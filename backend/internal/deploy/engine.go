package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"time"

	"zhan/backend/internal/command"
	"zhan/backend/internal/config"
	"zhan/backend/internal/database"
	"zhan/backend/internal/docker"
	"zhan/backend/internal/model"
	"zhan/backend/internal/notify"
)

type Engine struct {
	DB            *database.Store
	Docker        *docker.Client
	Config        config.Config
	Notify        *notify.Notifier
	mu            sync.Mutex
	workspaces    sync.Map
	cancels       map[int64]context.CancelFunc
	wg            sync.WaitGroup
	wake          chan struct{}
	notifications chan model.Task
}

func New(db *database.Store, d *docker.Client, c config.Config, n *notify.Notifier) *Engine {
	return &Engine{DB: db, Docker: d, Config: c, Notify: n, cancels: map[int64]context.CancelFunc{}, wake: make(chan struct{}, 1), notifications: make(chan model.Task, 64)}
}
func (e *Engine) Start(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(e.Config.DataDir, "logs"), 0700); err != nil {
		return err
	}
	if err := e.DB.Recover(); err != nil {
		return err
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		scan, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		items, err := e.Docker.Discover(scan)
		if err != nil {
			log.Printf("Compose discovery unavailable: %s", command.CleanError(err))
			return
		}
		log.Printf("Compose discovery: %d available projects (not imported)", len(items))
	}()
	for i := 0; i < e.Config.Workers; i++ {
		e.wg.Add(1)
		go e.worker(ctx)
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		cleanup := time.NewTicker(time.Hour)
		defer cleanup.Stop()
		e.cleanup()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.Poll()
				e.RetryDue()
			case <-cleanup.C:
				e.cleanup()
			}
		}
	}()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case task := <-e.notifications:
				if e.Notify != nil {
					stack, err := e.DB.Stack(task.StackID)
					if err == nil {
						e.Notify.Failure(task, stack)
					}
				}
			}
		}
	}()
	return nil
}
func (e *Engine) Wait() { e.wg.Wait() }
func (e *Engine) Enqueue(t *model.Task) error {
	if t.Trigger == "" {
		t.Trigger = "manual"
	}
	valid := map[string]bool{"git_check": true, "git_pull": true, "deploy": true, "rebuild": true, "start": true, "stop": true, "restart": true}
	if !valid[t.Type] {
		return errors.New("无效任务类型")
	}
	if (t.Service != "" || t.Container != "") && (t.Type != "start" && t.Type != "stop" && t.Type != "restart") {
		return errors.New("此操作仅支持整个 Stack")
	}
	if t.Service != "" && t.Container != "" {
		return errors.New("服务与容器目标不能同时指定")
	}
	stack, err := e.DB.Stack(t.StackID)
	if err != nil {
		return err
	}
	if err = e.Docker.Validate(&stack); err != nil {
		return err
	}
	if t.Trigger == "scheduled" && (t.Type == "deploy" || t.Type == "rebuild") && stack.Desired == "stopped" {
		return errors.New("项目已手动停止，暂停自动部署")
	}
	if err = e.DB.CreateTask(t); err != nil {
		return err
	}
	if t.Type == "stop" && t.Service == "" && t.Container == "" {
		_ = e.DB.SetDesired(stack.ID, "stopped")
	}
	select {
	case e.wake <- struct{}{}:
	default:
	}
	return nil
}
func (e *Engine) Cancel(id int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.DB.Task(id)
	if err != nil {
		return err
	}
	if t.Status != "pending" && t.Status != "running" {
		return errors.New("仅可取消排队或执行中的任务")
	}
	if t.Status == "pending" {
		_, err = e.DB.Exec("UPDATE tasks SET status='cancelled',cancel_requested=1,stage='cancelled',error_message='管理员取消任务',finished_at=? WHERE id=? AND status='pending'", database.Now(), id)
	} else {
		// Keep the active-task lock until the subprocess has actually terminated.
		_, err = e.DB.Exec("UPDATE tasks SET cancel_requested=1 WHERE id=? AND status='running'", id)
	}
	if cancel := e.cancels[id]; cancel != nil {
		cancel()
	}
	return err
}
func (e *Engine) worker(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		e.mu.Lock()
		t, err := e.DB.Claim()
		var taskCtx context.Context
		var cancel context.CancelFunc
		if err == nil {
			taskCtx, cancel = context.WithTimeout(ctx, time.Duration(e.Config.TaskTimeoutMinutes)*time.Minute)
			e.cancels[t.ID] = cancel
		}
		e.mu.Unlock()
		if err == nil {
			e.execute(taskCtx, &t)
			cancel()
			e.mu.Lock()
			delete(e.cancels, t.ID)
			e.mu.Unlock()
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("task claim: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-e.wake:
		}
	}
}
func (e *Engine) execute(ctx context.Context, t *model.Task) {
	stack, err := e.DB.Stack(t.StackID)
	if err != nil {
		t.Status = "failed"
		t.Error = command.CleanError(err)
		_ = e.DB.Finish(*t)
		return
	}
	workspace, _ := e.workspaces.LoadOrStore(stack.Directory, &sync.Mutex{})
	workspace.(*sync.Mutex).Lock()
	defer workspace.(*sync.Mutex).Unlock()
	out, err := newLog(e.LogPath(t.ID), e.Config.MaxLogMB)
	if err != nil {
		t.Status = "failed"
		t.Error = command.CleanError(err)
		_ = e.DB.Finish(*t)
		return
	}
	_, _ = e.DB.Exec("UPDATE tasks SET log_path=? WHERE id=?", e.LogPath(t.ID), t.ID)
	fmt.Fprintf(out, "任务 #%d · %s · %s\n", t.ID, stack.Name, t.Type)
	stage := func(name string) {
		t.Stage = name
		e.DB.Stage(t.ID, name)
		fmt.Fprintf(out, "\n[%s] %s\n", database.Now(), name)
	}
	err = e.run(ctx, t, &stack, out, stage)
	if logErr := out.Close(); err == nil && logErr != nil {
		err = logErr
	}
	if err == nil {
		t.Status = "success"
		t.Stage = "completed"
	} else {
		t.Status = "failed"
		t.ExitCode = -1
		if exit, ok := errors.AsType[*command.Error](err); ok {
			t.ExitCode = exit.Code
		}
		t.Error = command.CleanError(err)
		if ctx.Err() != nil {
			t.Error = "任务取消、服务关闭或执行超时：" + ctx.Err().Error()
			if errors.Is(ctx.Err(), context.Canceled) {
				t.Status = "cancelled"
			}
		}
	}
	// Preserve an explicit cancellation that arrived at the end of a command.
	e.mu.Lock()
	current, readErr := e.DB.Task(t.ID)
	if readErr == nil && (current.Status == "cancelled" || current.CancelRequested) {
		t.Status = "cancelled"
		t.Error = "管理员取消任务"
		t.Stage = "cancelled"
	}
	if t.Status == "failed" {
		appendLog, _ := newLog(e.LogPath(t.ID), e.Config.MaxLogMB)
		if appendLog != nil {
			fmt.Fprintf(appendLog, "\nERROR: %s\n", t.Error)
			_ = appendLog.Close()
		}
	}
	if (t.Type == "deploy" || t.Type == "rebuild") && t.Status == "failed" {
		e.recordFailure(stack, *t)
	}
	if err = e.DB.Finish(*t); err != nil {
		log.Printf("task finish: %v", err)
	}
	e.mu.Unlock()
	e.Docker.Invalidate()
	e.DB.Audit("worker", "task_"+t.Type, fmt.Sprint(t.ID), t.Status)
	if t.Status == "failed" {
		select {
		case e.notifications <- *t:
		default:
			log.Print("notification queue full; failure remains recorded")
		}
	}
	if t.Type == "git_check" && t.Trigger == "scheduled" && t.Status == "success" {
		e.maybeDeploy(stack.ID)
	}
}
func (e *Engine) run(ctx context.Context, t *model.Task, s *model.Stack, out io.Writer, stage func(string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.Docker.Validate(s); err != nil {
		return err
	}
	switch t.Type {
	case "git_check":
		stage("git_check")
		return ExplainGitError(e.checkGit(ctx, s, out))
	case "git_pull":
		stage("git_pull")
		return ExplainGitError(e.pullGit(ctx, s, out))
	case "start", "stop", "restart":
		stage(t.Type)
		services, err := e.Docker.CheckTarget(ctx, *s, t.Service, t.Container)
		if err != nil {
			return err
		}
		if t.Container != "" {
			if err = e.Docker.Runner.Run(ctx, s.Directory, "docker", []string{t.Type, t.Container}, out); err != nil {
				return err
			}
			if t.Type == "start" {
				return e.DB.SetDesired(s.ID, "running")
			}
			return nil
		}
		args := []string{t.Type}
		if t.Type == "start" {
			args = []string{"up", "-d", "--no-build", "--pull", "never"}
			if t.Service != "" {
				args = append(args, "--no-deps")
				for _, svc := range services {
					if svc.Name == t.Service {
						for _, dep := range svc.Dependencies {
							for _, candidate := range services {
								if candidate.Name == dep && candidate.Status != "running" {
									return fmt.Errorf("依赖服务 %s 未运行，请先启动依赖或整个项目", dep)
								}
							}
						}
					}
				}
			}
		}
		if t.Service != "" {
			args = append(args, t.Service)
		}
		if err = e.Docker.Exec(ctx, *s, out, args...); err != nil {
			return err
		}
		if t.Type == "start" {
			return e.DB.SetDesired(s.ID, "running")
		}
		return nil
	case "deploy", "rebuild":
		if t.Trigger == "scheduled" && s.Desired == "stopped" {
			return errors.New("期望状态为 stopped，自动部署已跳过")
		}
		if s.Desired == "stopped" && !t.StartAfter {
			return errors.New("项目已停止，请显式选择部署并启动")
		}
		if t.Type == "deploy" {
			stage("git_pull")
			if err := e.pullGit(ctx, s, out); err != nil {
				t.Commit = s.Remote
				if t.Commit == "" {
					t.Commit = s.Local
				}
				return ExplainGitError(err)
			}
		} else if s.Branch != "" {
			stage("git_check")
			if err := e.ValidateGit(ctx, s); err != nil {
				return ExplainGitError(err)
			}
			local, err := e.git(ctx, *s, "rev-parse", "HEAD")
			if err != nil {
				return ExplainGitError(err)
			}
			s.Local = stringsTrim(local)
			_, _ = e.DB.Exec("UPDATE deployment_configs SET local_commit=? WHERE stack_id=?", s.Local, s.ID)
		}
		t.Commit = s.Local
		_, _ = e.DB.Exec("UPDATE deployment_configs SET last_attempted_commit=? WHERE stack_id=?", t.Commit, s.ID)
		e.Docker.Invalidate()
		stage("config")
		services, err := e.Docker.Config(ctx, *s)
		if err != nil {
			return err
		}
		builds, images := []string{}, []string{}
		for _, svc := range services {
			if svc.Build {
				builds = append(builds, svc.Name)
			} else if svc.Image != "" {
				images = append(images, svc.Name)
			}
		}
		if len(images) > 0 && s.PullImages {
			stage("pull_images")
			if err = e.Docker.Exec(ctx, *s, out, append([]string{"pull", "--policy", "always"}, images...)...); err != nil {
				return err
			}
		}
		if len(builds) > 0 {
			stage("build")
			args := []string{"build"}
			if s.NoCache {
				args = append(args, "--no-cache")
			}
			if s.BuildPull {
				args = append(args, "--pull")
			}
			if err = e.Docker.Exec(ctx, *s, out, append(args, builds...)...); err != nil {
				return err
			}
		}
		// Recheck the durable intent immediately before touching running containers.
		fresh, err := e.DB.Stack(s.ID)
		if err != nil {
			return err
		}
		if fresh.Desired == "stopped" && !t.StartAfter {
			return errors.New("项目已停止，跳过容器启动")
		}
		stage("deploy")
		if err = e.Docker.Exec(ctx, *s, out, "up", "-d", "--no-build", "--pull", "never"); err != nil {
			return err
		}
		stage("healthcheck")
		e.Docker.Invalidate()
		if err = e.Docker.WaitHealthy(ctx, s); err != nil {
			return err
		}
		if s.Runtime != "running" {
			return fmt.Errorf("健康检查未通过：%s", s.Runtime)
		}
		_, err = e.DB.Exec("UPDATE deployment_configs SET last_success_commit=?,failure_count=0,failed_commit='',retry_at=0,retry_type='',retry_task_id=0 WHERE stack_id=?", t.Commit, s.ID)
		if err != nil {
			return err
		}
		return e.DB.SetDesired(s.ID, "running")
	}
	return errors.New("unknown task")
}
func stringsTrim(v string) string {
	for len(v) > 0 && (v[len(v)-1] == '\n' || v[len(v)-1] == '\r') {
		v = v[:len(v)-1]
	}
	return v
}

// recordFailure advances the backoff counter for a failed deployment and stores
// the action that failed so the retry repeats the same operation instead of
// being silently replaced by a plain pull.
func (e *Engine) recordFailure(s model.Stack, failed model.Task) {
	failures := s.Failures + 1
	if s.FailedCommit != failed.Commit {
		failures = 1
	}
	retry := int64(0)
	if delay, ok := e.Config.RetryDelay(failures); ok {
		retry = time.Now().Unix() + int64(delay.Seconds())
	}
	_, _ = e.DB.Exec("UPDATE deployment_configs SET last_attempted_commit=?,failed_commit=?,failure_count=?,retry_at=?,retry_type=?,retry_task_id=? WHERE stack_id=?", failed.Commit, failed.Commit, failures, retry, retryTaskType(failed), failed.ID, s.ID)
}

// retryTaskType keeps the failed action. A rebuild never pulls, so retrying it
// as a deploy would quietly change what the operator asked for. A deploy that
// failed during build still re-pulls on retry, which also picks up a fix pushed
// in the meantime.
func retryTaskType(t model.Task) string {
	if t.Type == "rebuild" {
		return "rebuild"
	}
	return "deploy"
}

// retryTask builds the follow-up action for a project whose backoff expired.
// When the remote branch already holds a newer commit, the retry pulls it first
// so a fix pushed in the meantime is picked up; otherwise it repeats the action
// that failed, which for a rebuild means the exact same working tree.
func retryTask(s model.Stack) model.Task {
	kind := s.RetryType
	if kind != "rebuild" && kind != "deploy" {
		kind = "deploy"
	}
	if s.Remote != "" && s.Remote != s.FailedCommit {
		kind = "deploy"
	}
	return model.Task{StackID: s.ID, Type: kind, Trigger: "scheduled", RetryOf: s.LastFailedTask}
}

// RetryDue enqueues retries whose backoff has expired. It runs on the scheduler
// tick, so a restart, a busy queue, or a cancelled retry cannot strand a failed
// deployment. Enqueue admits one active task per stack, and moving retry_at
// forward first stops two ticks from queueing the same attempt twice.
func (e *Engine) RetryDue() {
	items, err := e.DB.Stacks()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, s := range items {
		if s.Failures == 0 || s.RetryAt == 0 || s.RetryAt > now || s.Desired != "running" {
			continue
		}
		delay, ok := e.Config.RetryDelay(s.Failures)
		if !ok {
			// Retry budget spent: stop rescheduling until a new commit arrives.
			_, _ = e.DB.Exec("UPDATE deployment_configs SET retry_at=0 WHERE stack_id=?", s.ID)
			continue
		}
		// Claim the slot first so two ticks cannot both enqueue this attempt. The
		// conditional write also keeps a worker that finished in the meantime
		// from having its newer retry_at overwritten.
		claimed, err := e.DB.Exec("UPDATE deployment_configs SET retry_at=? WHERE stack_id=? AND retry_at=?", now+int64(delay.Seconds()), s.ID, s.RetryAt)
		if err != nil {
			continue
		}
		if n, err := claimed.RowsAffected(); err != nil || n == 0 {
			continue
		}
		task := retryTask(s)
		if err := e.Enqueue(&task); err != nil {
			// Give the attempt back so the next tick retries it: a busy stack is
			// only a temporary condition, and a rejected insert must not consume
			// the retry budget.
			_, _ = e.DB.Exec("UPDATE deployment_configs SET retry_at=? WHERE stack_id=?", s.RetryAt, s.ID)
			if !errors.Is(err, database.ErrBusy) {
				log.Printf("stack %d: retry not queued: %s", s.ID, command.CleanError(err))
			}
		}
	}
}
func (e *Engine) maybeDeploy(id int64) {
	s, err := e.DB.Stack(id)
	if err != nil || !s.Polling || !s.AutoDeploy || s.Desired != "running" || s.Remote == "" || s.Remote == s.Success {
		return
	}
	// A commit that already failed waits for its own backoff, which RetryDue
	// enforces with the action that actually failed. A newer commit is a fresh
	// attempt and is never held back by the old one.
	if s.FailedCommit == s.Remote && s.Failures > 0 && (s.RetryAt == 0 || s.RetryAt > time.Now().Unix()) {
		return
	}
	task := model.Task{StackID: id, Type: "deploy", Trigger: "scheduled"}
	_ = e.Enqueue(&task)
}
func (e *Engine) Poll() {
	items, err := e.DB.Stacks()
	if err != nil {
		return
	}
	for _, s := range items {
		if !s.Polling || s.Branch == "" || s.NextPoll > time.Now().Unix() {
			continue
		}
		task := model.Task{StackID: s.ID, Type: "git_check", Trigger: "scheduled"}
		if e.Enqueue(&task) == nil {
			_, _ = e.DB.Exec("UPDATE deployment_configs SET next_poll_at=? WHERE stack_id=?", time.Now().Unix()+int64(s.Interval)+int64(rand.IntN(10)), s.ID)
		}
	}
}
func (e *Engine) cleanup() {
	retention := e.Config.LogRetentionDays
	_ = e.DB.GetSetting("log_retention_days", &retention)
	if retention < 1 || retention > 365 {
		retention = e.Config.LogRetentionDays
	}
	cutoff := time.Now().Add(-time.Duration(retention) * 24 * time.Hour)
	entries, err := os.ReadDir(filepath.Join(e.Config.DataDir, "logs"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var id int64
		if _, err := fmt.Sscanf(entry.Name(), "task-%d.log", &id); err != nil {
			continue
		}
		task, err := e.DB.Task(id)
		if err != nil || task.Status == "pending" || task.Status == "running" {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(e.LogPath(id))
			_, _ = e.DB.Exec("UPDATE tasks SET log_path='' WHERE id=?", id)
		}
	}
	_, _ = e.DB.Exec("DELETE FROM sessions WHERE expires_at<?", time.Now().Unix())
}
