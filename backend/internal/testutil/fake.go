package testutil

import (
	"zhan/backend/internal/command"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

type Fake struct {
	Health     string
	Mu         sync.Mutex
	Calls      [][]string
	Project    string
	Running    bool
	FailBuild  bool
	FailUp     bool
	BlockBuild chan struct{}
	Started    chan struct{}
	ExitBuild  chan struct{}
}

func (f *Fake) Run(ctx context.Context, dir, name string, args []string, out io.Writer) error {
	if name == "git" {
		return command.OSRunner{}.Run(ctx, dir, name, args, out)
	}
	f.Mu.Lock()
	f.Calls = append(f.Calls, append([]string{name}, args...))
	running := f.Running
	healthOverride := f.Health
	project := f.Project
	failBuild, failUp := f.FailBuild, f.FailUp
	block, started, exit := f.BlockBuild, f.Started, f.ExitBuild
	f.Mu.Unlock()
	joined := strings.Join(args, " ")
	switch {
	case len(args) > 0 && args[0] == "ps":
		fmt.Fprintln(out, strings.Repeat("a", 64))
		fmt.Fprintln(out, strings.Repeat("b", 64))
		return nil
	case len(args) > 0 && args[0] == "inspect":
		state, health := "running", "healthy"
		if healthOverride != "" {
			health = healthOverride
		}
		if !running {
			state, health = "exited", "unhealthy"
		}
		items := []any{}
		for i, service := range []string{"api", "cache"} {
			id := strings.Repeat(string(rune('a'+i)), 64)
			items = append(items, map[string]any{"Id": id, "Name": "/" + project + "-" + service + "-1", "Config": map[string]any{"Image": "fixture-" + service, "Labels": map[string]string{"com.docker.compose.project": project, "com.docker.compose.service": service}}, "State": map[string]any{"Status": state, "Health": map[string]any{"Status": health}}, "NetworkSettings": map[string]any{"Ports": map[string]any{}}})
		}
		return json.NewEncoder(out).Encode(items)
	case strings.Contains(joined, "ls --all"):
		return json.NewEncoder(out).Encode([]map[string]string{{"Name": project, "Status": "running(2)", "ConfigFiles": "/untrusted/discovered/compose.yaml"}})
	case strings.Contains(joined, "config --format json"):
		_, err := io.WriteString(out, `{"services":{"api":{"build":{"context":"."},"image":"fixture-api","depends_on":{"cache":{"condition":"service_started"}}},"cache":{"image":"fixture-cache"}}}`)
		return err
	case strings.Contains(joined, " build"):
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
				if exit != nil {
					<-exit
				}
				return ctx.Err()
			}
		}
		if failBuild {
			fmt.Fprintln(out, "ERROR: password=super-secret-value build failed")
			return &command.Error{Code: 42, Cause: fmt.Errorf("fixture build failure")}
		}
		fmt.Fprintln(out, "build complete")
		return nil
	case strings.Contains(joined, " up "):
		if failUp {
			return &command.Error{Code: 17, Cause: fmt.Errorf("fixture deploy failure")}
		}
		f.Mu.Lock()
		f.Running = true
		f.Mu.Unlock()
		fmt.Fprintln(out, "containers running")
		return nil
	case strings.HasSuffix(joined, " stop"):
		f.Mu.Lock()
		f.Running = false
		f.Mu.Unlock()
		return nil
	case strings.Contains(joined, "logs"):
		fmt.Fprintln(out, "fixture output token=log-secret")
		return nil
	default:
		return nil
	}
}
func (f *Fake) Snapshot() [][]string {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	result := make([][]string, len(f.Calls))
	for i, call := range f.Calls {
		result[i] = append([]string{}, call...)
	}
	return result
}
