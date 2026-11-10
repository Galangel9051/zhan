package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zhan/backend/internal/config"
	"zhan/backend/internal/database"
	"zhan/backend/internal/docker"
	"zhan/backend/internal/model"
	"zhan/backend/internal/testutil"
)

func fixture(t *testing.T) (*Engine, *testutil.Fake, model.Stack) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services: {}"), 0600)
	db, err := database.Open(filepath.Join(t.TempDir(), "zhan.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fake := &testutil.Fake{Project: "fixture", Running: true}
	cfg := config.Config{DataDir: t.TempDir(), Workers: 2, TaskTimeoutMinutes: 1, LogRetentionDays: 30, MaxLogMB: 1, RetryBaseSeconds: 60, RetryMaxSeconds: 3600, RetryMaxAttempts: 5}
	d := &docker.Client{Runner: fake, Roots: []string{root}}
	e := New(db, d, cfg, nil)
	os.MkdirAll(filepath.Join(cfg.DataDir, "logs"), 0700)
	s := model.Stack{Name: "Fixture", ProjectName: "fixture", Directory: root, Files: []string{"compose.yaml"}, Desired: "running", Interval: 30, PullImages: true}
	if err = db.SaveStack(&s, true); err != nil {
		t.Fatal(err)
	}
	return e, fake, s
}
func executeNext(t *testing.T, e *Engine) model.Task {
	t.Helper()
	task, err := e.DB.Claim()
	if err != nil {
		t.Fatal(err)
	}
	e.execute(context.Background(), &task)
	saved, err := e.DB.Task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// pendingTasks keeps only queued work, so assertions about scheduling are not
// confused by the finished tasks that stay in the history.
func pendingTasks(tasks []model.Task) []model.Task {
	result := []model.Task{}
	for _, task := range tasks {
		if task.Status == "pending" {
			result = append(result, task)
		}
	}
	return result
}
func TestBuildFailurePreservesRunningVersion(t *testing.T) {
	e, fake, s := fixture(t)
	fake.FailBuild = true
	e.DB.Exec("UPDATE deployment_configs SET last_success_commit='previous' WHERE stack_id=?", s.ID)
	task := model.Task{StackID: s.ID, Type: "rebuild"}
	if err := e.Enqueue(&task); err != nil {
		t.Fatal(err)
	}
	saved := executeNext(t, e)
	if saved.Status != "failed" || saved.Stage != "build" || saved.ExitCode != 42 {
		t.Fatalf("wrong failure: %+v", saved)
	}
	for _, call := range fake.Snapshot() {
		if strings.Contains(strings.Join(call, " "), " up ") {
			t.Fatal("deployed after build failure")
		}
	}
	stored, err := e.DB.Stack(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Success != "previous" || stored.Failures != 1 || stored.RetryAt == 0 {
		t.Fatalf("failed commit state: %+v", stored)
	}
	if err = e.Docker.Status(context.Background(), &stored); err != nil || stored.Runtime != "running" {
		t.Fatal("build failure changed runtime state")
	}
	log, _, err := ReadLog(e.LogPath(task.ID), 0, 65536)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log, "super-secret-value") || !strings.Contains(log, "REDACTED") {
		t.Fatalf("unredacted log: %s", log)
	}
}
func TestStoppedStackNotAutoStarted(t *testing.T) {
	e, _, s := fixture(t)
	e.DB.Exec("UPDATE deployment_configs SET polling_enabled=1,auto_deploy=1,remote_commit='new',last_success_commit='old' WHERE stack_id=?", s.ID)
	e.DB.SetDesired(s.ID, "stopped")
	e.maybeDeploy(s.ID)
	tasks, _ := e.DB.Tasks(s.ID, 0, 10)
	if len(tasks) != 0 {
		t.Fatal("scheduled stopped project")
	}
	task := model.Task{StackID: s.ID, Type: "deploy", Trigger: "scheduled"}
	if err := e.Enqueue(&task); err == nil {
		t.Fatal("accepted scheduled stopped project")
	}
	e.DB.SetDesired(s.ID, "running")
	e.DB.Exec("UPDATE deployment_configs SET remote_commit='newer' WHERE stack_id=?", s.ID)
	e.maybeDeploy(s.ID)
	tasks, _ = e.DB.Tasks(s.ID, 0, 10)
	if len(tasks) != 1 || tasks[0].Type != "deploy" {
		t.Fatal("new commit did not restore deployment")
	}
}

// A manual rebuild that fails must still be retried automatically even when the
// project has no polling or auto deploy; the retry repeats an admitted action.
func TestManualRebuildRetriesWithoutPolling(t *testing.T) {
	e, fake, s := fixture(t)
	fake.FailBuild = true
	task := model.Task{StackID: s.ID, Type: "rebuild"}
	if err := e.Enqueue(&task); err != nil {
		t.Fatal(err)
	}
	if saved := executeNext(t, e); saved.Status != "failed" {
		t.Fatalf("rebuild should fail: %+v", saved)
	}
	e.DB.Exec("UPDATE deployment_configs SET retry_at=1 WHERE stack_id=?", s.ID)
	e.RetryDue()
	tasks, _ := e.DB.Tasks(s.ID, 0, 10)
	pending := pendingTasks(tasks)
	if len(pending) != 1 || pending[0].Type != "rebuild" {
		t.Fatalf("manual rebuild did not retry with polling off: %+v", tasks)
	}
}

// A failed build must be retried with the same action. A rebuild never pulls,
// so retrying it as a deploy would deploy code the operator never asked for.
func TestFailedRebuildRetriesAsRebuildWithExponentialBackoff(t *testing.T) {
	e, fake, s := fixture(t)
	fake.FailBuild = true
	e.DB.Exec("UPDATE deployment_configs SET polling_enabled=1,auto_deploy=1,local_commit='abc',remote_commit='abc' WHERE stack_id=?", s.ID)
	task := model.Task{StackID: s.ID, Type: "rebuild"}
	if err := e.Enqueue(&task); err != nil {
		t.Fatal(err)
	}
	if saved := executeNext(t, e); saved.Status != "failed" {
		t.Fatalf("rebuild should fail: %+v", saved)
	}
	stored, err := e.DB.Stack(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RetryType != "rebuild" || stored.LastFailedTask != task.ID {
		t.Fatalf("failed action not recorded: %+v", stored)
	}
	firstDelay := stored.RetryAt - time.Now().Unix()
	if firstDelay < 55 || firstDelay > 65 {
		t.Fatalf("first backoff should be about 60s, got %ds", firstDelay)
	}
	// Expire the backoff and let the scheduler queue the retry.
	e.DB.Exec("UPDATE deployment_configs SET retry_at=1 WHERE stack_id=?", s.ID)
	e.RetryDue()
	tasks, _ := e.DB.Tasks(s.ID, 0, 10)
	pending := pendingTasks(tasks)
	if len(pending) != 1 || pending[0].Type != "rebuild" {
		t.Fatalf("expected a pending rebuild retry: %+v", tasks)
	}
	if pending[0].RetryOf != task.ID {
		t.Fatalf("retry not linked to the failed task: %+v", pending[0])
	}
	// A second failure of the same commit doubles the wait.
	if saved := executeNext(t, e); saved.Status != "failed" {
		t.Fatalf("second rebuild should fail: %+v", saved)
	}
	stored, _ = e.DB.Stack(s.ID)
	if stored.Failures != 2 || stored.RetryType != "rebuild" {
		t.Fatalf("failure count not advanced: %+v", stored)
	}
	if secondDelay := stored.RetryAt - time.Now().Unix(); secondDelay < 115 || secondDelay > 125 {
		t.Fatalf("second backoff should be about 120s, got %ds", secondDelay)
	}
	// Exhausting the retry budget stops rescheduling instead of looping forever.
	for i := 0; i < 6; i++ {
		cur, _ := e.DB.Stack(s.ID)
		e.recordFailure(cur, model.Task{ID: task.ID, Type: "rebuild", Commit: "abc"})
	}
	stored, _ = e.DB.Stack(s.ID)
	if stored.RetryAt != 0 {
		t.Fatalf("retry budget exhausted but still scheduled: %+v", stored)
	}
	before, _ := e.DB.Tasks(s.ID, 0, 10)
	e.RetryDue()
	after, _ := e.DB.Tasks(s.ID, 0, 10)
	if len(after) != len(before) {
		t.Fatal("queued a retry after the budget was spent")
	}
}

// A deploy failure retries with a pull, so a fix pushed in the meantime lands.
func TestFailedDeployRetriesWithPull(t *testing.T) {
	e, fake, s := fixture(t)
	fake.FailUp = true
	e.DB.Exec("UPDATE deployment_configs SET polling_enabled=1,auto_deploy=1 WHERE stack_id=?", s.ID)
	task := model.Task{StackID: s.ID, Type: "deploy", Trigger: "scheduled"}
	if err := e.Enqueue(&task); err != nil {
		t.Fatal(err)
	}
	executeNext(t, e)
	// The fixture has no Git binding, so the deploy fails at the pull stage; the
	// retry must still be a deploy, which pulls again before building.
	stored, _ := e.DB.Stack(s.ID)
	if stored.RetryType != "deploy" || stored.Failures != 1 {
		t.Fatalf("deploy retry type lost: %+v", stored)
	}
	e.DB.Exec("UPDATE deployment_configs SET retry_at=1,remote_commit='newer' WHERE stack_id=?", s.ID)
	e.RetryDue()
	tasks, _ := e.DB.Tasks(s.ID, 0, 10)
	pending := pendingTasks(tasks)
	if len(pending) != 1 || pending[0].Type != "deploy" {
		t.Fatalf("expected a pending deploy retry that pulls: %+v", tasks)
	}
}

// A rebuild failure whose remote already moved on retries as a deploy, so the
// retry both re-pulls and rebuilds instead of replaying a stale working tree.
func TestRebuildRetrySwitchesToDeployWhenRemoteMoved(t *testing.T) {
	e, _, s := fixture(t)
	e.DB.Exec("UPDATE deployment_configs SET polling_enabled=1,auto_deploy=1 WHERE stack_id=?", s.ID)
	cur, _ := e.DB.Stack(s.ID)
	e.recordFailure(cur, model.Task{ID: 7, Type: "rebuild", Commit: "old"})
	e.DB.Exec("UPDATE deployment_configs SET retry_at=1,remote_commit='newer' WHERE stack_id=?", s.ID)
	e.RetryDue()
	tasks, _ := e.DB.Tasks(s.ID, 0, 10)
	pending := pendingTasks(tasks)
	if len(pending) != 1 || pending[0].Type != "deploy" {
		t.Fatalf("expected a deploy retry after the remote advanced: %+v", tasks)
	}
}

// A newer remote commit is never held back by the old commit's backoff.
func TestNewCommitIgnoresPreviousBackoff(t *testing.T) {
	e, _, s := fixture(t)
	e.DB.Exec("UPDATE deployment_configs SET polling_enabled=1,auto_deploy=1,remote_commit='new',last_success_commit='old',failed_commit='new',failure_count=3,retry_at=? WHERE stack_id=?", time.Now().Unix()+3600, s.ID)
	e.maybeDeploy(s.ID)
	tasks, _ := e.DB.Tasks(s.ID, 0, 10)
	if len(tasks) != 0 {
		t.Fatal("backoff should hold the failed commit")
	}
	e.DB.Exec("UPDATE deployment_configs SET remote_commit='newer' WHERE stack_id=?", s.ID)
	e.maybeDeploy(s.ID)
	tasks, _ = e.DB.Tasks(s.ID, 0, 10)
	if len(tasks) != 1 {
		t.Fatal("new commit was blocked by the old backoff")
	}
}
func TestCancelKeepsProjectLockUntilProcessExit(t *testing.T) {
	e, fake, s := fixture(t)
	fake.BlockBuild = make(chan struct{})
	fake.Started = make(chan struct{}, 1)
	fake.ExitBuild = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); e.Wait() }()
	task := model.Task{StackID: s.ID, Type: "rebuild"}
	if err := e.Enqueue(&task); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.Started:
	case <-time.After(5 * time.Second):
		t.Fatal("build did not start")
	}
	if err := e.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	second := model.Task{StackID: s.ID, Type: "rebuild"}
	if err := e.Enqueue(&second); !errors.Is(err, database.ErrBusy) {
		t.Fatalf("cancel prematurely released lock: %v", err)
	}
	close(fake.ExitBuild)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := e.DB.Task(task.ID)
		if current.Status == "cancelled" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cancellation did not finish")
}
func TestStopUsesStopAndPersistsIntent(t *testing.T) {
	e, fake, s := fixture(t)
	task := model.Task{StackID: s.ID, Type: "stop"}
	if err := e.Enqueue(&task); err != nil {
		t.Fatal(err)
	}
	stored, _ := e.DB.Stack(s.ID)
	if stored.Desired != "stopped" {
		t.Fatal("stop intent not persisted")
	}
	if saved := executeNext(t, e); saved.Status != "success" {
		t.Fatalf("stop failed: %+v", saved)
	}
	stop := false
	for _, call := range fake.Snapshot() {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, " down") {
			t.Fatal("used destructive down")
		}
		if strings.HasSuffix(joined, " stop") {
			stop = true
		}
	}
	if !stop {
		t.Fatal("did not call compose stop")
	}
}

func TestHealthFailureDoesNotAdvanceSuccessfulCommit(t *testing.T) {
	e, fake, s := fixture(t)
	fake.Health = "unhealthy"
	e.DB.Exec("UPDATE deployment_configs SET last_success_commit='previous' WHERE stack_id=?", s.ID)
	task := model.Task{StackID: s.ID, Type: "rebuild"}
	if err := e.Enqueue(&task); err != nil {
		t.Fatal(err)
	}
	saved := executeNext(t, e)
	if saved.Status != "failed" || saved.Stage != "healthcheck" {
		t.Fatalf("health failure stage lost: %+v", saved)
	}
	stored, _ := e.DB.Stack(s.ID)
	if stored.Success != "previous" {
		t.Fatal("unhealthy deployment marked successful")
	}
}
