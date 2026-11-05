package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/database"
	"zhan/backend/internal/docker"
	"zhan/backend/internal/model"
	"zhan/backend/internal/testutil"
)

type resourceRunner struct {
	base testutil.Fake
	fail bool
}

func (r *resourceRunner) Run(ctx context.Context, dir, name string, args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "stats" {
		if r.fail {
			return errors.New("private-daemon-error")
		}
		_, err := io.WriteString(out, `{"ID":"`+strings.Repeat("a", 64)+`","CPUPerc":"2.50%","MemPerc":"1.00%","MemUsage":"10MiB / 1GiB","NetIO":"1kB / 2kB","BlockIO":"0B / 1MiB","PIDs":"2"}`)
		return err
	}
	return r.base.Run(ctx, dir, name, args, out)
}
func TestContainerResourcesAuthenticationAndFailureIsolation(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, dir, 12)
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
	r := &resourceRunner{base: testutil.Fake{Project: "fixture", Running: true}}
	s := &Server{DB: db, Auth: a, Docker: &docker.Client{Runner: r}}
	router := s.Router()
	call := func(path string, authenticated bool, want int) string {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1"+path, nil)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "zhan_session", Value: token})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	call("/containers/stats", false, 401)
	if body := call("/containers/stats", true, 200); !strings.Contains(body, `"containers":{}`) || len(r.base.Snapshot()) != 0 {
		t.Fatal("unimported containers sampled")
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	p := model.Stack{Name: "Fixture", ProjectName: "fixture", Directory: dir, Files: []string{"compose.yaml"}, Desired: "running", Interval: 300}
	if err := db.SaveStack(&p, true); err != nil {
		t.Fatal(err)
	}
	if body := call("/containers/stats", true, 200); !strings.Contains(body, `"cpu_percent":2.5`) || !strings.Contains(body, `"unavailable_count":1`) {
		t.Fatalf("bad sample: %s", body)
	}
	r.fail = true
	s.Docker = &docker.Client{Runner: r}
	if body := call("/containers/stats", true, 503); strings.Contains(body, "private-daemon-error") {
		t.Fatal("internal Docker failure exposed")
	}
	if body := call("/stacks", true, 200); !strings.Contains(body, `"runtime_status":"running"`) {
		t.Fatal("stats failure changed runtime status")
	}
}
