package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compose-manager/backend/internal/command"
	"compose-manager/backend/internal/docker"
	"compose-manager/backend/internal/model"
)

// Opt-in: creates only its own uniquely named, portless Compose project.
func TestRealDockerLifecycle(t *testing.T) {
	if os.Getenv("CM_DOCKER_INTEGRATION") != "1" {
		t.Skip("set CM_DOCKER_INTEGRATION=1 to run the isolated Docker smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner := command.OSRunner{}
	if _, err := command.Capture(ctx, runner, "", "docker", "image", "inspect", "nginx:stable-alpine"); err != nil {
		t.Fatal("integration fixture requires local nginx:stable-alpine image: ", err)
	}
	e, _, s := fixture(t)
	s.ProjectName = fmt.Sprintf("cm-integration-%d", time.Now().UnixNano())
	s.Name = "Isolated integration fixture"
	s.PullImages = false
	content := "services:\n  web:\n    image: nginx:stable-alpine\n    healthcheck:\n      test: [CMD, wget, --spider, -q, 'http://127.0.0.1/']\n      interval: 1s\n      timeout: 2s\n      retries: 20\n"
	if err := os.WriteFile(filepath.Join(s.Directory, "compose.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.SaveStack(&s, false); err != nil {
		t.Fatal(err)
	}
	e.Docker = &docker.Client{Runner: runner, Roots: []string{s.Directory}}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if _, err := command.Capture(cleanup, runner, s.Directory, "docker", docker.Args(s, "down", "--remove-orphans")...); err != nil {
			t.Errorf("fixture cleanup failed: %v", err)
		}
	})
	run := func(kind, service, container string, start bool) {
		t.Helper()
		task := model.Task{StackID: s.ID, Type: kind, Service: service, Container: container, StartAfter: start}
		if err := e.Enqueue(&task); err != nil {
			t.Fatal(err)
		}
		claimed, err := e.DB.Claim()
		if err != nil {
			t.Fatal(err)
		}
		e.execute(ctx, &claimed)
		saved, _ := e.DB.Task(claimed.ID)
		if saved.Status != "success" {
			log, _, _ := ReadLog(e.LogPath(task.ID), 0, 65536)
			t.Fatalf("%s failed: %+v\n%s", kind, saved, log)
		}
	}
	run("start", "", "", false)
	if err := e.Docker.Status(ctx, &s); err != nil || s.Runtime != "running" {
		t.Fatalf("start runtime: %s %v", s.Runtime, err)
	}
	run("stop", "", "", false)
	stored, _ := e.DB.Stack(s.ID)
	if stored.Desired != "stopped" {
		t.Fatal("stop intent missing")
	}
	if err := e.Docker.Status(ctx, &s); err != nil || s.Runtime != "stopped" {
		t.Fatalf("stop runtime: %s %v", s.Runtime, err)
	}
	run("rebuild", "", "", true) // image-only project: no source build, no Git operation.
	run("restart", "web", "", false)
	e.Docker.Invalidate()
	if err := e.Docker.Status(ctx, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Services) != 1 || len(s.Services[0].Containers) != 1 {
		t.Fatalf("invalid container inventory: %+v", s.Services)
	}
	id := s.Services[0].Containers[0].ID
	run("stop", "", id, false)
	run("start", "", id, false)
	logs, err := command.Capture(ctx, runner, s.Directory, "docker", docker.Args(s, "logs", "--no-color", "--tail", "20", "web")...)
	if err != nil || !strings.Contains(logs, "nginx") {
		t.Fatalf("runtime logs unavailable: %v", err)
	}
}
