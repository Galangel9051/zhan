package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/command"
	"zhan/backend/internal/database"
	"zhan/backend/internal/docker"
)

func TestGitBranchInspection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repository")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git(repo, "init", "-b", "main")
	git(repo, "commit", "--allow-empty", "-m", "fixture")
	git(repo, "branch", "feature/local")
	git(repo, "remote", "add", "origin", "https://private-user:private-token@example.invalid/repository.git")
	git(repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(repo, "update-ref", "refs/remotes/origin/release", "HEAD")
	git(repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	head := git(repo, "rev-parse", "HEAD")

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
	d := &docker.Client{Runner: command.OSRunner{}, Roots: []string{root}}
	router := (&Server{DB: db, Auth: a, Docker: d}).Router()
	call := func(path string, authenticated bool, want int) gitBranches {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/git/branches?path="+url.QueryEscape(path), nil)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "zhan_session", Value: token})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("got %d, want %d: %s", w.Code, want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-token") || strings.Contains(w.Body.String(), "private-user") {
			t.Fatal("remote credentials exposed")
		}
		var response struct {
			Data gitBranches `json:"data"`
		}
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
		}
		return response.Data
	}
	call(repo, false, 401)
	call("relative/path", true, 400)
	call(t.TempDir(), true, 400)
	call(filepath.Join(root, "missing"), true, 400)
	plain := call(root, true, 200)
	if plain.IsRepository || len(plain.Branches) != 0 {
		t.Fatal("plain directory detected as repository")
	}
	result := call(repo, true, 200)
	if !result.IsRepository || !result.HasOrigin || result.CurrentBranch != "main" || !reflect.DeepEqual(result.Branches, []string{"feature/local", "main", "release"}) {
		t.Fatalf("incorrect branches: %+v", result)
	}
	if git(repo, "symbolic-ref", "--short", "HEAD") != "main" || git(repo, "rev-parse", "HEAD") != head || git(repo, "status", "--porcelain") != "" {
		t.Fatal("inspection modified the working tree")
	}
	worktree := filepath.Join(root, "worktree")
	git(repo, "worktree", "add", "-b", "linked", worktree)
	if got := call(worktree, true, 200); got.CurrentBranch != "linked" || !got.IsRepository {
		t.Fatalf("linked worktree not recognized: %+v", got)
	}
	git(repo, "checkout", "--detach")
	if got := call(repo, true, 200); got.CurrentBranch != "" || !got.IsRepository {
		t.Fatalf("detached HEAD not recognized: %+v", got)
	}
	git(repo, "remote", "remove", "origin")
	if call(repo, true, 200).HasOrigin {
		t.Fatal("missing origin not recognized")
	}
}
