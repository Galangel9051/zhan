package proxy

import (
	"zhan/backend/internal/auth"
	"zhan/backend/internal/command"
	"zhan/backend/internal/database"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu       sync.Mutex
	calls    []string
	failTest bool
	failUp   bool
	upCount  int
}

func (f *fakeRunner) Run(ctx context.Context, dir, name string, args []string, w io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if strings.Contains(cmd, " run ") {
		if f.failTest {
			io.WriteString(w, "nginx: configuration test failed")
			return errors.New("nginx -t failed")
		}
		io.WriteString(w, "syntax is ok")
		return nil
	}
	if strings.Contains(cmd, " up ") {
		f.upCount++
		if f.failUp {
			f.failUp = false
			return errors.New("port occupied")
		}
	}
	if strings.Contains(cmd, " ps ") {
		io.WriteString(w, `{"State":"running"}`)
	}
	return nil
}
func setup(t *testing.T, r command.Runner) *Manager {
	t.Helper()
	dir := t.TempDir()
	db, e := database.Open(filepath.Join(dir, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := auth.New(db, dir, 12)
	if e != nil {
		t.Fatal(e)
	}
	m := New(db, a, r, dir)
	if e = m.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close(); db.Close() })
	return m
}
func fixture() Config {
	c := Defaults()
	c.Hosts = []Host{{ID: "0123456789abcdef", Domain: "app.home.duckdns.org", Upstream: "http://192.168.1.20:3000", Name: "LAN", Enabled: true, Websocket: true}}
	return c
}
func TestValidationAndGeneratedConfig(t *testing.T) {
	for _, target := range []string{"http://a.test;evil", "http://a.test/$variable", "http://a.test/abc", "ftp://a.test", "http://user:pass@a.test", "http://127.0.0.1:80", "http://a.test:99999", "http://a.test\n}", "http://a.test?x=1"} {
		c := fixture()
		c.Hosts[0].Upstream = target
		if Normalize(&c) == nil {
			t.Errorf("accepted unsafe upstream %q", target)
		}
	}
	c := fixture()
	c.Hosts[0].HTTPS = true
	c.Hosts[0].Redirect = true
	c.Settings.HTTPSPort = 8443
	if e := Normalize(&c); e != nil {
		t.Fatal(e)
	}
	out := Render(c)
	for _, want := range []string{"proxy_pass http://192.168.1.20:3000;", "https://app.home.duckdns.org:8443$request_uri", "ssl_protocols TLSv1.2 TLSv1.3", "proxy_set_header Upgrade", "server_name app.home.duckdns.org;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	c.Hosts[0].CertExpires = "keep-me"
	fingerprint(c)
	if c.Hosts[0].CertExpires != "keep-me" {
		t.Fatal("fingerprint mutated certificate state")
	}
	c.Hosts[0].Domain = "x.test; return 200;"
	if Normalize(&c) == nil {
		t.Fatal("domain injection accepted")
	}
}
func TestPersistenceSecretsRevisionAndRollback(t *testing.T) {
	r := &fakeRunner{}
	m := setup(t, r)
	c := fixture()
	c.Settings.DuckToken = "very-secret-token"
	saved, e := m.Save(c)
	if e != nil {
		t.Fatal(e)
	}
	if saved.Settings.DuckToken != "" || !saved.Settings.TokenSet {
		t.Fatal("secret returned")
	}
	var raw string
	m.DB.QueryRow("SELECT value FROM app_settings WHERE key='proxy_config'").Scan(&raw)
	if strings.Contains(raw, "very-secret-token") {
		t.Fatal("unencrypted token")
	}
	if _, e = m.Save(c); e == nil {
		t.Fatal("stale revision accepted")
	}
	saved.Hosts[0].Name = "changed"
	if saved, e = m.Save(saved); e != nil {
		t.Fatal(e)
	}
	loaded, _ := m.load()
	token, e := m.Auth.Decrypt(loaded.Settings.DuckToken)
	if e != nil || token != "very-secret-token" {
		t.Fatal("blank token did not preserve secret")
	}
	if _, e = m.execute(context.Background(), "apply", Host{}, loaded); e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(filepath.Join(m.Dir, "conf", "nginx.conf"))
	loaded.Hosts[0].Upstream = "http://192.168.1.30:4000"
	r.failTest = true
	if _, e = m.execute(context.Background(), "apply", Host{}, loaded); e == nil {
		t.Fatal("validation failure ignored")
	}
	current, _ := os.ReadFile(filepath.Join(m.Dir, "conf", "nginx.conf"))
	if string(original) != string(current) {
		t.Fatal("failed test replaced live config")
	}
	r.failTest = false
	r.failUp = true
	if _, e = m.execute(context.Background(), "apply", Host{}, loaded); e == nil || !strings.Contains(e.Error(), "已恢复旧配置") {
		t.Fatalf("rollback not reported: %v", e)
	}
	current, _ = os.ReadFile(filepath.Join(m.Dir, "conf", "nginx.conf"))
	if string(original) != string(current) || r.upCount != 3 {
		t.Fatal("old config was not restored")
	}
	state, e := m.State(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(state.Settings.DuckToken, "secret") {
		t.Fatal("state exposed token")
	}
}
func TestJobsAndCertificatePrerequisites(t *testing.T) {
	m := setup(t, &fakeRunner{})
	c := fixture()
	c.Hosts[0].HTTPS = true
	c, _ = m.Save(c)
	if _, e := m.Enqueue("certificate", c.Hosts[0].ID, "admin"); e == nil {
		t.Fatal("issued without credentials")
	}
	if _, e := m.Enqueue("invalid", "", "admin"); e == nil {
		t.Fatal("invalid action")
	}
	if _, e := m.Enqueue("test", "", "admin"); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s, e := m.State(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if !s.Busy {
			if len(s.Jobs) != 1 || s.Jobs[0].Status != "failed" || !strings.Contains(s.Jobs[0].Message, "证书") {
				t.Fatalf("unexpected job: %+v", s.Jobs)
			}
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("job did not finish")
}
