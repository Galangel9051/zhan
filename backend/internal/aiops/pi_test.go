package aiops

import (
	"context"
	"encoding/json"
	"io"
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

func TestLegacyControllerPermissionDoesNotExposeRemovedTool(t *testing.T) {
	m, j := fixture(t)
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["allow_clash"] = true
	raw, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	for _, item := range toolsFor(j) {
		function := item.(map[string]any)["function"].(map[string]any)
		if function["name"] == "clash" {
			t.Fatal("legacy permission exposed removed tool")
		}
	}
	used := false
	if _, err := m.callTool(t.Context(), j, 0, nil, toolCallFrom("clash", `{}`), &used); err == nil {
		t.Fatal("removed tool was executable")
	}
}
