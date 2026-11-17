package aiops

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zhan/backend/internal/command"
)

type commandProbe struct{ args []string }

func (p *commandProbe) Run(ctx context.Context, dir, name string, args []string, out io.Writer) error {
	p.args = args
	_, _ = io.WriteString(out, strings.Repeat("x", 40*1024))
	return &command.Error{Code: 7}
}
func TestPiCommandPermissionsAndIsolation(t *testing.T) {
	m, j := fixture(t)
	p := &commandProbe{}
	m.Docker.Runner = p
	if _, err := m.runCommand(context.Background(), j, json.RawMessage(`{"command":"python test.py"}`)); err == nil {
		t.Fatal("unauthorized command accepted")
	}
	j.Engine = "pi"
	j.AllowCommands = true
	v, err := m.runCommand(context.Background(), j, json.RawMessage(`{"command":"python test.py"}`))
	if err != nil {
		t.Fatal(err)
	}
	r := v.(map[string]any)
	if r["exit_code"] != 7 || r["truncated"] != true || len(r["output"].(string)) != 32*1024 {
		t.Fatalf("bad result: %v", r["exit_code"])
	}
	args := strings.Join(p.args, " ")
	for _, want := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "--pids-limit=128", "target=/workspace,readonly"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(args, "docker.sock") {
		t.Fatal("docker socket exposed")
	}
	j.AllowNetwork = true
	j.Writable = true
	_, err = m.runCommand(context.Background(), j, json.RawMessage(`{"command":"true"}`))
	if err != nil {
		t.Fatal(err)
	}
	args = strings.Join(p.args, " ")
	if strings.Contains(args, "--network=none") || strings.Contains(args, "/workspace,readonly") {
		t.Fatal("authorized capabilities not applied")
	}
}
func TestClashAuthorizationEncryptionAndOperations(t *testing.T) {
	m, j := fixture(t)
	var selected string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer clash-secret" {
			t.Error("missing auth")
		}
		switch {
		case r.Method == "PUT" && r.URL.Path == "/proxies/PROXY":
			var b struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			selected = b.Name
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/delay"):
			_, _ = io.WriteString(w, `{"delay":12}`)
		default:
			_, _ = io.WriteString(w, `{"proxies":{"PROXY":{"now":"bad","all":["bad","good"]}}}`)
		}
	}))
	defer server.Close()
	if err := m.SaveClash(ClashSettings{BaseURL: server.URL, Secret: "clash-secret", Groups: []string{"PROXY"}}); err != nil {
		t.Fatal(err)
	}
	safe, _ := m.ClashSettings(false)
	if safe.Secret != "" || !safe.SecretSet {
		t.Fatal("secret exposed")
	}
	var stored ClashSettings
	_ = m.DB.GetSetting("ai_clash", &stored)
	if stored.Secret == "clash-secret" {
		t.Fatal("unencrypted secret")
	}
	if _, err := m.clashTool(context.Background(), j, json.RawMessage(`{"operation":"inspect"}`)); err == nil {
		t.Fatal("unauthorized access")
	}
	j.AllowClash = true
	for _, args := range []string{`{"operation":"inspect"}`, `{"operation":"delay","node":"good"}`, `{"operation":"select","group":"PROXY","node":"good"}`} {
		if _, err := m.clashTool(context.Background(), j, json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
	}
	if selected != "good" {
		t.Fatal("selection not applied")
	}
	if _, err := m.clashTool(context.Background(), j, json.RawMessage(`{"operation":"select","group":"other","node":"bad"}`)); err == nil {
		t.Fatal("unapproved group accepted")
	}
	if err := m.SaveClash(ClashSettings{BaseURL: server.URL + "/other"}); err == nil {
		t.Fatal("secret reused for changed endpoint")
	}
}
