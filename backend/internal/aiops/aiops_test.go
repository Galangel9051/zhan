package aiops

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/config"
	"zhan/backend/internal/database"
	"zhan/backend/internal/deploy"
	"zhan/backend/internal/docker"
	"zhan/backend/internal/model"
	"zhan/backend/internal/testutil"
)

func fixture(t *testing.T) (*Manager, Job) {
	t.Helper()
	data := t.TempDir()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(data, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, err := auth.New(db, data, 12)
	if err != nil {
		t.Fatal(err)
	}
	d := &docker.Client{Roots: []string{workspace}, Runner: &testutil.Fake{Project: "fixture", Running: true}}
	e := deploy.New(db, d, config.Config{DataDir: data, Workers: 1, TaskTimeoutMinutes: 1, MaxLogMB: 1, LogRetentionDays: 30}, nil)
	m := New(db, a, d, e)
	j := Job{Name: "巡检", Prompt: "检查并修复配置", Workspace: workspace, Schedule: "manual", Timeout: 30, MaxSteps: 5, Timezone: "Asia/Shanghai"}
	if err = m.SaveJob(&j); err != nil {
		t.Fatal(err)
	}
	return m, j
}
func provider(t *testing.T, m *Manager, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if err := m.SaveSettings(Settings{Enabled: true, BaseURL: server.URL + "/v1", Model: "fixture", APIKey: "secret-key"}); err != nil {
		t.Fatal(err)
	}
	return server
}
func assistant(w http.ResponseWriter, content string, calls ...map[string]any) {
	reason := "stop"
	if len(calls) > 0 {
		reason = "tool_calls"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": reason, "message": map[string]any{"role": "assistant", "content": content, "tool_calls": calls}}}})
}
func call(name, args string) map[string]any {
	return map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": name, "arguments": args}}
}
func TestAgentReadsWritesAndPersists(t *testing.T) {
	m, j := fixture(t)
	j.Writable = true
	if err := m.SaveJob(&j); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j.Workspace, "config.txt"), []byte("before"), 0640); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	provider(t, m, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer secret-key" {
			t.Error("invalid request")
		}
		var body struct {
			Messages []message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch count.Add(1) {
		case 1:
			assistant(w, "", call("read_file", `{"path":"config.txt"}`))
		case 2:
			last := body.Messages[len(body.Messages)-1]
			if last.Role != "tool" || !strings.Contains(last.Content, "before") {
				t.Error("missing tool result")
			}
			assistant(w, "", call("write_file", fmt.Sprintf(`{"path":"config.txt","content":"after","expected_sha256":"%x"}`, sha256.Sum256([]byte("before")))))
		default:
			assistant(w, "配置已更新。")
		}
	})
	id, err := m.Enqueue(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.work(context.Background())
	r, err := m.Run(id)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(j.Workspace, "config.txt"))
	if r.Status != "success" || string(data) != "after" || !strings.Contains(r.Output, "read_file") || !strings.Contains(r.Output, "配置已更新") {
		t.Fatalf("run=%+v data=%s", r, data)
	}
	info, _ := os.Stat(filepath.Join(j.Workspace, "config.txt"))
	if info.Mode().Perm() != 0640 {
		t.Fatal("file permissions changed")
	}
	safe, err := m.Settings(false)
	if err != nil || safe.APIKey != "" || !safe.KeySet {
		t.Fatalf("leaked settings %+v %v", safe, err)
	}
	var stored Settings
	if err = m.DB.GetSetting("ai_provider", &stored); err != nil {
		t.Fatal(err)
	}
	if stored.APIKey == "secret-key" {
		t.Fatal("plaintext API key")
	}
	if err = m.SaveSettings(Settings{Enabled: true, BaseURL: safe.BaseURL, Model: safe.Model}); err != nil {
		t.Fatal(err)
	}
	secret, err := m.Settings(true)
	if err != nil || secret.APIKey != "secret-key" {
		t.Fatal("blank key did not preserve secret")
	}
	listed, err := m.Runs(0)
	if err != nil || len(listed) != 1 || listed[0].Output != "" {
		t.Fatal("list should omit full logs")
	}
}
func TestWorkspaceBoundariesPermissionsAndConflicts(t *testing.T) {
	m, j := fixture(t)
	root, err := os.OpenRoot(j.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	outside := t.TempDir()
	if err = os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(j.Workspace, "link")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(j.Workspace, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret", "/etc/passwd", "link/secret", ".git/config"} {
		if _, err = readFile(root, name); err == nil {
			t.Fatalf("read allowed %q", name)
		}
		if _, err = writeFile(root, name, "bad", ""); err == nil {
			t.Fatalf("write allowed %q", name)
		}
	}
	if _, err = writeFile(root, "new.txt", "original", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = writeFile(root, "new.txt", "overwrite", ""); err == nil {
		t.Fatal("blind overwrite allowed")
	}
	if _, err = writeFile(root, "new.txt", "overwrite", "incorrect"); err == nil {
		t.Fatal("stale hash allowed")
	}
	if _, err = writeFile(root, "large.txt", strings.Repeat("x", maxFileBytes+1), ""); err == nil {
		t.Fatal("large file allowed")
	}
	used := false
	if _, err = m.callTool(context.Background(), j, 1, root, toolCallFrom("write_file", `{"path":"bad.txt","content":"bad","expected_sha256":""}`), &used); err == nil {
		t.Fatal("read-only job wrote a file")
	}
	if _, err = m.callTool(context.Background(), j, 1, root, toolCallFrom("stack_action", `{"action":"stop"}`), &used); err == nil {
		t.Fatal("unauthorized action")
	}
	j.Workspace = m.Engine.Config.DataDir
	if err = m.SaveJob(&j); err == nil {
		t.Fatal("platform data accessible")
	}
}
func toolCallFrom(name, args string) toolCall {
	var c toolCall
	c.ID = "id"
	c.Type = "function"
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}
func TestDurableSchedulesEventsAndNoRecursion(t *testing.T) {
	m, j := fixture(t)
	j.Schedule = "interval"
	j.Interval = 60
	j.Enabled = true
	if err := m.SaveJob(&j); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(61 * time.Second)
	if err := m.schedule(now); err != nil {
		t.Fatal(err)
	}
	if err := m.schedule(now); err != nil {
		t.Fatal(err)
	}
	runs, err := m.Runs(0)
	if err != nil || len(runs) != 1 || runs[0].JobID != j.ID {
		t.Fatalf("runs %+v err %v", runs, err)
	}
	if err = m.Cancel(runs[0].ID); err != nil {
		t.Fatal(err)
	}
	j.Schedule = "event"
	j.EventName = "monitor.alert"
	if err = m.SaveJob(&j); err != nil {
		t.Fatal(err)
	}
	if err = m.Emit("other.event", 0, "ignore"); err != nil {
		t.Fatal(err)
	}
	if err = m.Emit(j.EventName, 0, "disk full"); err != nil {
		t.Fatal(err)
	}
	if err = m.schedule(time.Now()); err != nil {
		t.Fatal(err)
	}
	runs, err = m.Runs(0)
	if err != nil || len(runs) != 2 || runs[0].Trigger != "event" || !strings.Contains(runs[0].Event, "disk full") {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	if err = m.Cancel(runs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = m.schedule(time.Now()); err != nil {
		t.Fatal(err)
	}
	var n int
	m.DB.QueryRow("SELECT count(*) FROM ai_runs").Scan(&n)
	if n != 2 {
		t.Fatal("event duplicated")
	}
	stack := model.Stack{Name: "fixture", ProjectName: "fixture", Directory: j.Workspace, Files: []string{"compose.yaml"}, Desired: "running", Interval: 300}
	if err = m.DB.SaveStack(&stack, true); err != nil {
		t.Fatal(err)
	}
	for _, aiID := range []int64{0, 123} {
		task := model.Task{StackID: stack.ID, Type: "restart", Trigger: "manual", AIRunID: aiID}
		if err = m.DB.CreateTask(&task); err != nil {
			t.Fatal(err)
		}
		task.Status = "failed"
		task.Error = "test failure"
		if err = m.DB.Finish(task); err != nil {
			t.Fatal(err)
		}
	}
	m.DB.QueryRow("SELECT count(*) FROM ai_events WHERE name='task.failed'").Scan(&n)
	if n != 1 {
		t.Fatalf("recursive event or missing event: %d", n)
	}
}
func TestDailyTimezoneAndRestartRecovery(t *testing.T) {
	m, j := fixture(t)
	j.Schedule = "daily"
	j.DailyAt = "09:00"
	j.Timezone = "Asia/Shanghai"
	now := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	want := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC).Unix()
	if j.next(now) != want {
		t.Fatalf("next=%v want=%v", j.next(now), want)
	}
	provider(t, m, func(w http.ResponseWriter, r *http.Request) { assistant(w, "ok") })
	id, err := m.Enqueue(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.DB.Exec("UPDATE ai_runs SET status='running' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	run, err := m.Run(id)
	if err != nil || run.Status != "cancelled" {
		t.Fatalf("recovery=%+v err=%v", run, err)
	}
}
func TestStepLimitAndCancellation(t *testing.T) {
	m, j := fixture(t)
	j.MaxSteps = 1
	if err := m.SaveJob(&j); err != nil {
		t.Fatal(err)
	}
	provider(t, m, func(w http.ResponseWriter, r *http.Request) { assistant(w, "", call("list_files", `{"path":"."}`)) })
	id, err := m.Enqueue(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.work(context.Background())
	run, _ := m.Run(id)
	if run.Status != "failed" || !strings.Contains(run.Error, "步数") {
		t.Fatalf("limit=%+v", run)
	}
	// Hold the deployment gate and verify cancellation also interrupts an AI task waiting for it.
	entered := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = m.Engine.WithWorkspace(context.Background(), j.Workspace, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	defer close(release)
	id, err = m.Enqueue(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.work(context.Background()); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		run, _ = m.Run(id)
		if run.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err = m.Cancel(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt waiting worker")
	}
	run, _ = m.Run(id)
	if run.Status != "cancelled" {
		t.Fatal(run.Status)
	}
}

func TestEventStackFilterEnableBoundaryAndActionPolicy(t *testing.T) {
	m, j := fixture(t)
	if err := os.WriteFile(filepath.Join(j.Workspace, "compose.yaml"), []byte("services: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	stack := model.Stack{Name: "fixture", ProjectName: "fixture", Directory: j.Workspace, Files: []string{filepath.Join(j.Workspace, "compose.yaml")}, Desired: "stopped", Interval: 300}
	if err := m.DB.SaveStack(&stack, true); err != nil {
		t.Fatal(err)
	}
	j.StackID = stack.ID
	j.AllowActions = true
	j.Schedule = "event"
	j.EventName = "monitor.alert"
	j.Enabled = true
	if err := m.Emit(j.EventName, stack.ID, "historical"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveJob(&j); err != nil {
		t.Fatal(err)
	}
	if err := m.Emit(j.EventName, 0, "global"); err != nil {
		t.Fatal(err)
	}
	if err := m.schedule(time.Now()); err != nil {
		t.Fatal(err)
	}
	runs, _ := m.Runs(0)
	if len(runs) != 0 {
		t.Fatal("historical or unrelated event fired")
	}
	if err := m.Emit(j.EventName, stack.ID, "current"); err != nil {
		t.Fatal(err)
	}
	if err := m.schedule(time.Now()); err != nil {
		t.Fatal(err)
	}
	runs, _ = m.Runs(0)
	if len(runs) != 1 {
		t.Fatal("matching event not scheduled")
	}
	root, err := os.OpenRoot(j.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	used := false
	for _, action := range []string{"deploy", "rebuild", "start", "restart"} {
		if _, err = m.callTool(context.Background(), j, runs[0].ID, root, toolCallFrom("stack_action", fmt.Sprintf(`{"action":%q}`, action)), &used); err == nil {
			t.Fatalf("AI restarted stopped stack via %s", action)
		}
	}
	value, err := m.callTool(context.Background(), j, runs[0].ID, root, toolCallFrom("stack_action", `{"action":"git_check"}`), &used)
	if err != nil {
		t.Fatal(err)
	}
	taskID := value.(map[string]any)["task_id"].(int64)
	task, err := m.DB.Task(taskID)
	if err != nil || task.AIRunID != runs[0].ID {
		t.Fatalf("lost AI provenance: %+v %v", task, err)
	}
	if _, err = m.callTool(context.Background(), j, runs[0].ID, root, toolCallFrom("stack_action", `{"action":"git_check"}`), &used); err == nil {
		t.Fatal("second operation allowed")
	}
	// Platform files remain off limits even when their parent is an otherwise allowed root.
	m.Docker.Roots = nil
	j.Workspace = filepath.Dir(m.Engine.Config.DataDir)
	j.AllowActions = false
	if err = m.SaveJob(&j); err == nil {
		t.Fatal("workspace containing platform state allowed")
	}
}
func TestProviderErrorsAndDisableCancelsActive(t *testing.T) {
	m, j := fixture(t)
	provider(t, m, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("upstream echoed secret-key"))
	})
	id, err := m.Enqueue(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.work(context.Background())
	run, _ := m.Run(id)
	if run.Status != "failed" || strings.Contains(run.Error, "secret-key") {
		t.Fatalf("upstream error leaked: %+v", run)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = m.Engine.WithWorkspace(context.Background(), j.Workspace, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	defer close(release)
	id, err = m.Enqueue(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.work(context.Background()); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		run, _ = m.Run(id)
		if run.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not start")
		}
		time.Sleep(time.Millisecond)
	}
	s, _ := m.Settings(false)
	s.Enabled = false
	if err = m.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disabling provider did not cancel active task")
	}
	if _, err = m.Enqueue(j.ID); err == nil {
		t.Fatal("disabled provider admitted task")
	}
}
