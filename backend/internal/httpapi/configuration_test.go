package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/config"
	"zhan/backend/internal/database"
	"zhan/backend/internal/deploy"
	"zhan/backend/internal/docker"
	"zhan/backend/internal/model"
	"zhan/backend/internal/notify"
	"zhan/backend/internal/proxy"
	"zhan/backend/internal/testutil"
)

func TestConfigurationExportPreviewAndImport(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
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
	token, u, err := a.Login("admin", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	fake := &testutil.Fake{Project: "fixture"}
	d := &docker.Client{Runner: fake, Roots: []string{root}}
	n := &notify.Notifier{DB: db, Auth: a}
	cfg := config.Config{DataDir: data, LogRetentionDays: 30}
	e := deploy.New(db, d, cfg, n)
	m := proxy.New(db, a, fake, data)
	defer m.Close()
	mail := model.SMTP{Host: "smtp.example.invalid", Port: 587, Username: "alerts", Password: "fixture-smtp-secret", TLS: "starttls", From: "alerts@example.invalid", Recipients: []string{"ops@example.invalid"}, Stages: []string{"deploy"}, Cooldown: 30}
	if err := n.Save(mail); err != nil {
		t.Fatal(err)
	}
	gateway := proxy.Defaults()
	gateway.Settings.DuckDomain, gateway.Settings.DuckToken = "home.duckdns.org", "fixture-duck-secret"
	gateway.Hosts = []proxy.Host{{ID: "0123456789abcdef", Domain: "app.home.duckdns.org", Upstream: "http://192.168.1.20:3000", Enabled: true}}
	if _, err := m.Save(gateway); err != nil {
		t.Fatal(err)
	}
	project := model.Stack{Name: "Fixture", ProjectName: "fixture", Directory: root, Files: []string{filepath.Join(root, "compose.yaml")}, Desired: "running", Interval: 300, Notify: true}
	if err := db.SaveStack(&project, true); err != nil {
		t.Fatal(err)
	}
	router := (&Server{DB: db, Auth: a, Docker: d, Engine: e, Notify: n, Proxy: m, Config: cfg}).Router()
	call := func(method, endpoint string, payload any, session, csrf string, want int) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if raw, ok := payload.(string); ok {
			body = []byte(raw)
		}
		req := httptest.NewRequest(method, "/api/v1/settings/config/"+endpoint, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "zhan_session", Value: session})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, endpoint, w.Code, want, w.Body.String())
		}
		return w
	}
	call("GET", "export", nil, "", "", 401)
	call("POST", "import/preview", nil, token, "", 403)
	call("POST", "import", nil, token, "", 403)
	export := call("GET", "export", nil, token, "", 200)
	if !strings.Contains(export.Header().Get("Content-Disposition"), ".json") || export.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("export must download JSON without caching")
	}
	for _, secret := range []string{"fixture-smtp-secret", "fixture-duck-secret", "password_hash", "sessions", "services", "desired_state"} {
		if strings.Contains(export.Body.String(), secret) {
			t.Fatalf("non-configuration data exported: %s", secret)
		}
	}
	var backup configurationBackup
	if err := json.Unmarshal(export.Body.Bytes(), &backup); err != nil {
		t.Fatal(err)
	}
	if len(backup.Projects) != 1 || backup.SMTP.PasswordSet || backup.Proxy.Settings.TokenSet || backup.Proxy.Revision != 0 {
		t.Fatal("incorrect exported configuration")
	}
	decode := func(body []byte) configurationPreview {
		t.Helper()
		var result struct {
			Data configurationPreview `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		return result.Data
	}
	currentRevision := func() string {
		t.Helper()
		r, err := db.ConfigurationRevision(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	backup.SMTP.Enabled = new(false)
	disabledPreview := decode(call("POST", "import/preview", backup, token, u.CSRF, 200).Body.Bytes())
	if disabledPreview.SMTPEnabled {
		t.Fatal("disabled SMTP reported enabled in import preview")
	}
	backup.SMTP.Enabled = nil

	before := currentRevision()
	backup.System.Retention = 60
	backup.Projects[0].Name = "Restored fixture"
	second := backup.Projects[0]
	second.Name, second.ProjectName = "Second", "second"
	backup.Projects = append(backup.Projects, second)
	preview := decode(call("POST", "import/preview", backup, token, u.CSRF, 200).Body.Bytes())
	if preview.Created != 1 || preview.Updated != 1 || preview.ProxyHosts != 1 || preview.Revision != before || currentRevision() != before {
		t.Fatalf("incorrect or mutating preview: %+v", preview)
	}
	payload := map[string]any{"backup": backup, "revision": preview.Revision}
	call("POST", "import", payload, token, u.CSRF, 200)
	call("POST", "import", payload, token, u.CSRF, 409)
	got, err := db.Stack(project.ID)
	if err != nil || got.Name != "Restored fixture" || got.Desired != "running" || got.Polling || got.AutoDeploy {
		t.Fatalf("incorrect restored project: %+v (%v)", got, err)
	}
	var retention int
	if err := db.GetSetting("log_retention_days", &retention); err != nil || retention != 60 {
		t.Fatal("system setting was not restored")
	}
	var stored proxy.Config
	if err := db.GetSetting("proxy_config", &stored); err != nil {
		t.Fatal(err)
	}
	if secret, err := a.Decrypt(stored.Settings.DuckToken); err != nil || secret != "fixture-duck-secret" {
		t.Fatal("same-domain local Token was not preserved")
	}
	if saved, err := n.Settings(); err != nil || !saved.PasswordSet {
		t.Fatal("same-connection local SMTP password was not preserved")
	}
	// Invalid input must never write any of the otherwise valid settings.
	for _, mutate := range []func(*configurationBackup){
		func(b *configurationBackup) { b.Version = 2 },
		func(b *configurationBackup) { b.System = nil },
		func(b *configurationBackup) { b.SMTP.Password = "do-not-accept-secret" },
		func(b *configurationBackup) { b.Proxy.Settings.DuckToken = "do-not-accept-secret" },
		func(b *configurationBackup) { b.Proxy.Settings.HTTPPort = 0 },
		func(b *configurationBackup) { b.System.Retention = 366 },
		func(b *configurationBackup) { b.Projects = append(b.Projects, b.Projects[0]) },
		func(b *configurationBackup) { b.Projects[0].Directory = filepath.Join(root, "missing") },
	} {
		encoded, _ := json.Marshal(backup)
		var bad configurationBackup
		json.Unmarshal(encoded, &bad)
		mutate(&bad)
		revision := currentRevision()
		call("POST", "import/preview", bad, token, u.CSRF, 400)
		call("POST", "import", map[string]any{"backup": bad, "revision": revision}, token, u.CSRF, 400)
		if revision != currentRevision() {
			t.Fatal("invalid backup changed settings")
		}
	}
	call("POST", "import/preview", `{"unknown":true}`, token, u.CSRF, 400)
	call("POST", "import/preview", `{} {}`, token, u.CSRF, 400)
	call("POST", "import/preview", strings.Repeat(" ", maxConfigurationBytes)+"{}", token, u.CSRF, 413)
	// A credential must not be reused for a different endpoint or domain.
	backup.SMTP.Host = "smtp.other.invalid"
	backup.Proxy.Settings.DuckDomain = "other.duckdns.org"
	preview = decode(call("POST", "import/preview", backup, token, u.CSRF, 200).Body.Bytes())
	if len(preview.Warnings) < 4 {
		t.Fatal("missing credential re-entry notices")
	}
	call("POST", "import", map[string]any{"backup": backup, "revision": preview.Revision}, token, u.CSRF, 200)
	if saved, err := n.Settings(); err != nil || saved.PasswordSet {
		t.Fatal("old password reused for a different server")
	}
	stored = proxy.Config{}
	if err := db.GetSetting("proxy_config", &stored); err != nil || stored.Settings.DuckToken != "" {
		t.Fatal("old Token reused for a different domain")
	}
	var tasks int
	if err := db.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks); err != nil || tasks != 0 {
		t.Fatal("restore scheduled work")
	}
	for _, call := range fake.Snapshot() {
		if !strings.Contains(strings.Join(call, " "), "config --format json") {
			t.Fatalf("restore ran a mutating Docker command: %v", call)
		}
	}
}
