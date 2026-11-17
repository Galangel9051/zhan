package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"zhan/backend/internal/aiops"
	"zhan/backend/internal/auth"
	"zhan/backend/internal/config"
	"zhan/backend/internal/database"
	"zhan/backend/internal/deploy"
	"zhan/backend/internal/docker"
)

func TestAIAuthenticationCSRFAndLifecycle(t *testing.T) {
	data := t.TempDir()
	workspace := t.TempDir()
	db, err := database.Open(filepath.Join(data, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, data, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Init("admin", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	token, u, err := a.Login("admin", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	d := &docker.Client{}
	e := deploy.New(db, d, config.Config{DataDir: data}, nil)
	manager := aiops.New(db, a, d, e)
	router := (&Server{DB: db, Auth: a, Docker: d, Engine: e, AI: manager}).Router()
	call := func(method, path string, payload any, session, csrf bool, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, "/api/v1/ai"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if session {
			req.AddCookie(&http.Cookie{Name: "zhan_session", Value: token})
		}
		if csrf {
			req.Header.Set("X-CSRF-Token", u.CSRF)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	call("GET", "/settings", nil, false, false, 401)
	call("PUT", "/settings", map[string]any{}, true, false, 403)
	call("PUT", "/settings", aiops.Settings{Enabled: true, BaseURL: "http://localhost:9999/v1", Model: "fixture", APIKey: "test-secret"}, true, true, 200)
	safe := call("GET", "/settings", nil, true, false, 200)
	if bytes.Contains(safe, []byte("test-secret")) {
		t.Fatal("API leaked key")
	}
	job := aiops.Job{Name: "Test", Prompt: "检查工作区", Workspace: workspace, Schedule: "manual", Timeout: 30, MaxSteps: 5}
	var saved struct {
		Data aiops.Job `json:"data"`
	}
	if err = json.Unmarshal(call("POST", "/jobs", job, true, true, 200), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Data.ID == 0 {
		t.Fatal("job not saved")
	}
	var run struct {
		Data aiops.Run `json:"data"`
	}
	if err = json.Unmarshal(call("POST", "/jobs/1/run", nil, true, true, 202), &run); err != nil {
		t.Fatal(err)
	}
	call("POST", "/jobs/1/run", nil, true, true, 409)
	call("PUT", "/jobs/1", saved.Data, true, true, 400)
	call("POST", "/runs/1/cancel", nil, true, false, 403)
	call("POST", "/runs/1/cancel", nil, true, true, 204)
	call("GET", "/runs/1", nil, true, false, 200)
	call("GET", "/runs?before=-1", nil, true, false, 400)
	call("POST", "/events", map[string]any{"name": "monitor.alert", "payload": "CPU high"}, true, true, 204)
	call("POST", "/events", map[string]any{"name": "task.failed"}, true, true, 400)
	job.AllowActions = true
	call("POST", "/jobs", job, true, true, 400)
}
