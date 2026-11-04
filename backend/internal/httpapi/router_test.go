package httpapi

import (
	"bytes"
	"compose-manager/backend/internal/auth"
	"compose-manager/backend/internal/config"
	"compose-manager/backend/internal/database"
	"compose-manager/backend/internal/deploy"
	"compose-manager/backend/internal/docker"
	"compose-manager/backend/internal/model"
	"compose-manager/backend/internal/notify"
	"compose-manager/backend/internal/testutil"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuthenticatedImportTaskLogsAndSettings(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services: {}"), 0600)
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	a.Init("admin", "correct-password-1234")
	fake := &testutil.Fake{Project: "fixture", Running: true}
	cfg := config.Config{DataDir: dir, AllowedRoots: []string{root}, Workers: 1, SessionHours: 12, TaskTimeoutMinutes: 1, LogRetentionDays: 30, MaxLogMB: 1}
	d := &docker.Client{Runner: fake, Roots: cfg.AllowedRoots}
	n := &notify.Notifier{DB: db, Auth: a}
	engine := deploy.New(db, d, cfg, n)
	ctx, cancel := context.WithCancel(context.Background())
	if err = engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); engine.Wait() }()
	server := httptest.NewServer((&Server{DB: db, Auth: a, Docker: d, Engine: engine, Notify: n, Config: cfg}).Router())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	csrf := ""
	call := func(method, path string, payload any, want int, token string, origin string) []byte {
		t.Helper()
		var body io.Reader
		if payload != nil {
			data, _ := json.Marshal(payload)
			body = bytes.NewReader(data)
		}
		req, _ := http.NewRequest(method, server.URL+"/api/v1"+path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", token)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.StatusCode, want, data)
		}
		return data
	}
	call("GET", "/stacks", nil, 401, "", "")
	call("POST", "/auth/login", map[string]string{"username": "admin", "password": "correct-password-1234"}, 403, "", "https://evil.invalid")
	data := call("POST", "/auth/login", map[string]string{"username": "admin", "password": "correct-password-1234"}, 200, "", "")
	var session struct{ Data auth.User }
	json.Unmarshal(data, &session)
	csrf = session.Data.CSRF
	if csrf == "" {
		t.Fatal("no CSRF token")
	}
	cookies := jar.Cookies(mustURL(t, server.URL))
	if len(cookies) != 1 {
		t.Fatal("session cookie missing")
	}
	form := map[string]any{"name": "Fixture", "project_name": "fixture", "project_directory": root, "compose_files": []string{"compose.yaml"}}
	call("POST", "/stacks/import", form, 403, "", "")
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	badForm := map[string]any{"name": "Outside", "project_name": "outside", "project_directory": outside, "compose_files": []string{"compose.yaml"}}
	failure := call("POST", "/stacks/import", badForm, 400, csrf, "")
	for _, detail := range []string{"项目路径校验失败", "allowed_roots", root, outside} {
		if !strings.Contains(string(failure), detail) {
			t.Fatalf("missing actionable import detail %q: %s", detail, failure)
		}
	}
	badForm["project_directory"] = filepath.Join(root, "missing")
	failure = call("POST", "/stacks/import", badForm, 400, csrf, "")
	if !strings.Contains(string(failure), "路径不存在") {
		t.Fatalf("missing nonexistent-path explanation: %s", failure)
	}
	data = call("POST", "/stacks/import", form, 201, csrf, "")
	var imported struct{ Data model.Stack }
	if err = json.Unmarshal(data, &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Data.ID != 1 || imported.Data.Desired != "running" {
		t.Fatalf("invalid import: %s", data)
	}
	for _, call := range fake.Snapshot() {
		joined := strings.Join(call, " ")
		for _, word := range []string{" up ", " stop", " build", " restart", " down"} {
			if strings.Contains(joined, word) {
				t.Fatalf("import mutated Docker: %v", call)
			}
		}
	}
	data = call("POST", "/stacks/1/rebuild", map[string]any{}, 202, csrf, "")
	var created struct{ Data model.Task }
	json.Unmarshal(data, &created)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := db.Task(created.Data.ID)
		if task.Status == "success" {
			break
		}
		if task.Status == "failed" {
			t.Fatalf("task failed: %+v", task)
		}
		time.Sleep(10 * time.Millisecond)
	}
	data = call("GET", "/tasks/1/stream", nil, 200, "", "")
	if !strings.Contains(string(data), "event:log") || !strings.Contains(string(data), "event:done") {
		t.Fatalf("SSE missing task events: %s", data)
	}
	call("GET", "/tasks/1/logs?offset=0&limit=1024", nil, 200, "", "")
	call("GET", "/tasks/1/logs?download=1", nil, 200, "", "")
	smtp := model.SMTP{Host: "smtp.example.invalid", Port: 587, Username: "fixture", Password: "never-return-this-password", TLS: "starttls", From: "sender@example.invalid", Recipients: []string{"recipient@example.invalid"}, Stages: []string{"build"}, Cooldown: 30}
	data = call("PUT", "/settings/smtp", smtp, 200, csrf, "")
	if strings.Contains(string(data), smtp.Password) {
		t.Fatal("SMTP password returned")
	}
	data = call("GET", "/settings", nil, 200, "", "")
	if strings.Contains(string(data), smtp.Password) || !strings.Contains(string(data), `"password_set":true`) {
		t.Fatal("bad password masking")
	}
	temporarySMTP := smtp
	temporarySMTP.TLS = "invalid"
	testError := call("POST", "/settings/smtp/test", map[string]any{"config": temporarySMTP}, 400, csrf, "")
	if !strings.Contains(string(testError), "测试邮件发送失败") || !strings.Contains(string(testError), "TLS") {
		t.Fatalf("test error lacks reason: %s", testError)
	}
	var persistedSMTP model.SMTP
	if err := db.GetSetting("smtp", &persistedSMTP); err != nil || persistedSMTP.TLS != "starttls" {
		t.Fatal("test overwrote saved SMTP config")
	}
	var encrypted string
	db.QueryRow("SELECT ciphertext FROM credentials WHERE name='smtp_password'").Scan(&encrypted)
	if encrypted == "" || strings.Contains(encrypted, smtp.Password) {
		t.Fatal("credential not encrypted")
	}
	call("PUT", "/settings", map[string]int{"log_retention_days": 7}, 204, csrf, "")
	call("GET", "/audit", nil, 200, "", "")
	call("POST", "/auth/logout", map[string]any{}, 204, csrf, "")
	call("GET", "/auth/me", nil, 401, "", "")
}

func mustURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestBootstrapPasswordGate(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.InitDefault("admin"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{SessionHours: 12}
	server := httptest.NewServer((&Server{DB: db, Auth: a, Config: cfg}).Router())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := ""
	call := func(method, path string, payload any, want int, token string) []byte {
		t.Helper()
		var body io.Reader
		if payload != nil {
			data, _ := json.Marshal(payload)
			body = bytes.NewReader(data)
		}
		req, _ := http.NewRequest(method, server.URL+"/api/v1"+path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.StatusCode, want, data)
		}
		return data
	}
	data := call("POST", "/auth/login", map[string]string{"username": "admin", "password": "123456"}, 200, "")
	var session struct{ Data auth.User }
	json.Unmarshal(data, &session)
	csrf = session.Data.CSRF
	if !session.Data.MustChangePassword {
		t.Fatal("missing forced password change")
	}
	call("GET", "/auth/me", nil, 200, "")
	for _, path := range []string{"/stacks", "/stacks/discover", "/tasks", "/settings", "/audit", "/notifications", "/filesystem"} {
		call("GET", path, nil, 403, "")
	}
	call("POST", "/stacks/1/start", map[string]any{}, 403, csrf)
	password := map[string]string{"current_password": "123456", "new_password": "abcdef", "confirm_password": "different"}
	call("POST", "/auth/password", password, 403, "")
	call("POST", "/auth/password", password, 400, csrf)
	password["confirm_password"] = password["new_password"]
	call("POST", "/auth/password", password, 204, csrf)
	call("GET", "/auth/me", nil, 401, "")
	call("POST", "/auth/login", map[string]string{"username": "admin", "password": "123456"}, 401, "")
	data = call("POST", "/auth/login", map[string]string{"username": "admin", "password": password["new_password"]}, 200, "")
	json.Unmarshal(data, &session)
	if session.Data.MustChangePassword {
		t.Fatal("password gate remained after change")
	}
	call("GET", "/tasks", nil, 200, "")
}
