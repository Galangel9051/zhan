package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/command"
	"zhan/backend/internal/database"
	"zhan/backend/internal/docker"
	"zhan/backend/internal/model"
)

func TestGitHistoryRealMergeAndMissingRemote(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "main")
	git("commit", "--allow-empty", "-m", "根提交")
	base := git("rev-parse", "HEAD")
	git("checkout", "-b", "feature")
	git("commit", "--allow-empty", "-m", "功能: a | b")
	feature := git("rev-parse", "HEAD")
	git("checkout", "main")
	git("commit", "--allow-empty", "-m", "主分支更新")
	git("merge", "--no-ff", "feature", "-m", "合并功能")
	head := git("rev-parse", "HEAD")
	item := model.Stack{Directory: root, Branch: "main", Remote: feature, Success: base, Attempted: head}
	got, err := readGitHistory(context.Background(), command.OSRunner{}, item)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commits) != 4 || got.Commits[0].Hash != head || len(got.Commits[0].Parents) != 2 || got.Local != head || got.Branch != "main" || got.Dirty || got.Ahead == nil || *got.Ahead != 2 || *got.Behind != 0 {
		t.Fatalf("incorrect graph: %+v", got)
	}
	for _, commit := range got.Commits {
		if commit.Author != "Fixture" || commit.AuthoredAt == "" {
			t.Fatalf("missing metadata: %+v", commit)
		}
	}
	// Diverged histories are represented as two tips, not a fabricated single line.
	git("checkout", "feature")
	git("commit", "--allow-empty", "-m", "远程后续提交")
	item.Remote = git("rev-parse", "HEAD")
	git("checkout", "main")
	got, err = readGitHistory(context.Background(), command.OSRunner{}, item)
	if err != nil || len(got.Commits) != 5 || got.Ahead == nil || *got.Ahead != 2 || *got.Behind != 1 {
		t.Fatalf("divergence: %+v, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked"), []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	item.Remote = strings.Repeat("f", 40)
	got, err = readGitHistory(context.Background(), command.OSRunner{}, item)
	if err != nil || !got.Dirty || got.Remote != item.Remote || got.Ahead != nil || got.Behind != nil {
		t.Fatalf("missing remote: %+v, %v", got, err)
	}
	if git("rev-parse", "HEAD") != head || git("symbolic-ref", "--short", "HEAD") != "main" || git("status", "--porcelain") != "?? untracked" {
		t.Fatal("history view changed working tree")
	}
	git("update-ref", "refs/remotes/origin/main", head)
	item.Remote = ""
	got, err = readGitHistory(context.Background(), command.OSRunner{}, item)
	if err != nil || got.Remote != head || got.RemoteSource != "cached" {
		t.Fatalf("cached remote: %+v %v", got, err)
	}
	for range 51 {
		git("commit", "--allow-empty", "-m", "历史")
	}
	got, err = readGitHistory(context.Background(), command.OSRunner{}, item)
	if err != nil || !got.Truncated || len(got.Commits) != 50 {
		t.Fatalf("history bound: %+v %v", got, err)
	}

	data := t.TempDir()
	db, err := database.Open(filepath.Join(data, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, data, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Init("admin", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := a.Login("admin", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	item.Desired = "running"
	item.Name, item.ProjectName, item.Files, item.Interval = "fixture", "fixture", []string{"compose.yaml"}, 300
	if err := db.SaveStack(&item, true); err != nil {
		t.Fatal(err)
	}
	router := (&Server{DB: db, Auth: a, Docker: &docker.Client{Runner: command.OSRunner{}, Roots: []string{root}}}).Router()
	call := func(id int64, authenticated bool, status int) string {
		t.Helper()
		req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/stacks/%d/git/history", id), nil)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "zhan_session", Value: token})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("got %d want %d: %s", w.Code, status, w.Body.String())
		}
		return w.Body.String()
	}
	call(item.ID, false, 401)
	call(999, true, 404)
	var response struct {
		Data gitHistory `json:"data"`
	}
	if err := json.Unmarshal([]byte(call(item.ID, true, 200)), &response); err != nil || len(response.Data.Commits) != 50 {
		t.Fatalf("API history: %v", err)
	}
	item.Branch = ""
	if err := db.SaveStack(&item, false); err != nil {
		t.Fatal(err)
	}
	call(item.ID, true, 400)
	item.Branch = "main"
	item.Directory = t.TempDir()
	if err := db.SaveStack(&item, false); err != nil {
		t.Fatal(err)
	}
	call(item.ID, true, 400)
}

func TestGitHistoryUnbornBranch(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-b", "main", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	got, err := readGitHistory(context.Background(), command.OSRunner{}, model.Stack{Directory: root, Branch: "main"})
	if err != nil || got.Local != "" || len(got.Commits) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}
