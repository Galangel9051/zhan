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
	cfg := config.Config{DataDir: t.TempDir(), Workers: 2, TaskTimeoutMinutes: 1, LogRetentionDays: 30, MaxLogMB: 1, RetryDelays: []int{300, 900}}
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
func TestStoppedStackNotAutoStartedAndRetryBounded(t *testing.T) {
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
	s.Failures = 0
	s.FailedCommit = ""
	e.recordFailure(s, "new")
	s, _ = e.DB.Stack(s.ID)
	e.recordFailure(s, "new")
	s, _ = e.DB.Stack(s.ID)
	e.recordFailure(s, "new")
	s, _ = e.DB.Stack(s.ID)
	if s.Failures != 3 || s.RetryAt != 0 {
		t.Fatal("retry did not pause after threshold")
	}
	e.maybeDeploy(s.ID)
	tasks, _ = e.DB.Tasks(s.ID, 0, 10)
	if len(tasks) != 0 {
		t.Fatal("unbounded retry")
	}
	e.DB.Exec("UPDATE deployment_configs SET remote_commit='newer' WHERE stack_id=?", s.ID)
	e.maybeDeploy(s.ID)
	tasks, _ = e.DB.Tasks(s.ID, 0, 10)
	if len(tasks) != 1 || tasks[0].Type != "deploy" {
		t.Fatal("new commit did not restore deployment")
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
