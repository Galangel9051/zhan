package deploy

import (
	"zhan/backend/internal/command"
	"zhan/backend/internal/model"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := command.Capture(context.Background(), command.OSRunner{}, dir, "git", args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}
func TestGitFastForwardDirtyTreeAndDeploymentVersions(t *testing.T) {
	e, fake, s := fixture(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitTest(t, "", "init", "--bare", remote)
	gitTest(t, s.Directory, "init", "--initial-branch=main")
	gitTest(t, s.Directory, "config", "user.name", "Fixture")
	gitTest(t, s.Directory, "config", "user.email", "fixture@example.invalid")
	gitTest(t, s.Directory, "add", "compose.yaml")
	gitTest(t, s.Directory, "commit", "-m", "initial")
	gitTest(t, s.Directory, "remote", "add", "origin", remote)
	gitTest(t, s.Directory, "push", "-u", "origin", "main")
	initial := gitTest(t, s.Directory, "rev-parse", "HEAD")
	s.Branch = "main"
	if err := e.ValidateGit(context.Background(), &s); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.SaveStack(&s, false); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "writer")
	gitTest(t, "", "clone", "--branch", "main", remote, other)
	gitTest(t, other, "config", "user.name", "Fixture")
	gitTest(t, other, "config", "user.email", "fixture@example.invalid")
	os.WriteFile(filepath.Join(other, "version.txt"), []byte("new version"), 0600)
	gitTest(t, other, "add", "version.txt")
	gitTest(t, other, "commit", "-m", "remote update")
	gitTest(t, other, "push", "origin", "main")
	latest := gitTest(t, other, "rev-parse", "HEAD")
	check := model.Task{StackID: s.ID, Type: "git_check"}
	e.Enqueue(&check)
	if saved := executeNext(t, e); saved.Status != "success" {
		t.Fatalf("git check failed: %+v", saved)
	}
	if gitTest(t, s.Directory, "rev-parse", "HEAD") != initial {
		t.Fatal("check mutated working tree")
	}
	pull := model.Task{StackID: s.ID, Type: "git_pull"}
	e.Enqueue(&pull)
	if saved := executeNext(t, e); saved.Status != "success" {
		t.Fatalf("pull failed: %+v", saved)
	}
	stored, _ := e.DB.Stack(s.ID)
	if stored.Local != latest || stored.Success != "" {
		t.Fatal("pull incorrectly marked deployment successful")
	}
	for _, call := range fake.Snapshot() {
		if strings.Contains(strings.Join(call, " "), " up ") {
			t.Fatal("git pull deployed containers")
		}
	}
	fake.Mu.Lock()
	fake.FailBuild = true
	fake.Mu.Unlock()
	deploy := model.Task{StackID: s.ID, Type: "deploy"}
	e.Enqueue(&deploy)
	if saved := executeNext(t, e); saved.Status != "failed" || saved.Stage != "build" {
		t.Fatalf("build failure not preserved: %+v", saved)
	}
	stored, _ = e.DB.Stack(s.ID)
	if stored.Attempted != latest || stored.Success != "" || stored.FailedCommit != latest {
		t.Fatalf("commit accounting incorrect: %+v", stored)
	}
	fake.Mu.Lock()
	fake.FailBuild = false
	fake.Mu.Unlock()
	retry := model.Task{StackID: s.ID, Type: "deploy"}
	e.Enqueue(&retry)
	if saved := executeNext(t, e); saved.Status != "success" {
		t.Fatalf("retry failed: %+v", saved)
	}
	stored, _ = e.DB.Stack(s.ID)
	if stored.Success != latest || stored.Failures != 0 {
		t.Fatal("successful retry did not advance deployed version")
	}
	os.WriteFile(filepath.Join(s.Directory, "version.txt"), []byte("local modifications"), 0600)
	dirty := model.Task{StackID: s.ID, Type: "git_pull"}
	e.Enqueue(&dirty)
	if saved := executeNext(t, e); saved.Status != "failed" || !strings.Contains(saved.Error, "本地修改") {
		t.Fatalf("dirty tree accepted: %+v", saved)
	}
	data, _ := os.ReadFile(filepath.Join(s.Directory, "version.txt"))
	if string(data) != "local modifications" {
		t.Fatal("overwrote dirty tree")
	}
}
