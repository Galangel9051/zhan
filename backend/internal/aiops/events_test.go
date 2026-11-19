package aiops

import (
	"strings"
	"testing"
	"time"
	"zhan/backend/internal/model"
)

func TestBuiltinEventValidationAndReservedNamespaces(t *testing.T) {
	seen := map[string]bool{}
	for _, event := range EventDefinitions() {
		if seen[event.Name] || !validEvent(event.Name) || !reservedEvent(event.Name) || event.Label == "" || event.Description == "" {
			t.Fatalf("invalid definition: %+v", event)
		}
		seen[event.Name] = true
	}
	if len(seen) != 26 {
		t.Fatalf("expected 26 builtins, got %d", len(seen))
	}
	for _, name := range []string{"task.typo", "git.typo", "deploy.typo", "project.typo", "container.typo", "bad name", "bad\nname", ""} {
		if validEvent(name) {
			t.Fatalf("accepted invalid %q", name)
		}
	}
	if !validEvent("monitor.alert") || reservedEvent("monitor.alert") {
		t.Fatal("custom events rejected")
	}
	m, _ := fixture(t)
	for name := range seen {
		if err := m.Emit(name, 0, "spoofed"); err == nil {
			t.Fatalf("allowed forged builtin %s", name)
		}
	}
	if err := m.Emit("monitor.alert", 0, "fixture"); err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentEventSchedulesAssociatedJobOnlyOnce(t *testing.T) {
	m, j := fixture(t)
	stack := model.Stack{Name: "events", ProjectName: "events", Directory: j.Workspace, Files: []string{"compose.yaml"}, Desired: "running", Interval: 300}
	if err := m.DB.SaveStack(&stack, true); err != nil {
		t.Fatal(err)
	}
	j.Schedule = "event"
	j.EventName = "deploy.failed"
	j.Enabled = true
	j.StackID = stack.ID
	if err := m.SaveJob(&j); err != nil {
		t.Fatal(err)
	}
	task := model.Task{StackID: stack.ID, Type: "deploy", Trigger: "manual"}
	if err := m.DB.CreateTask(&task); err != nil {
		t.Fatal(err)
	}
	task.Status = "failed"
	task.Error = "build failed"
	if err := m.DB.Finish(task); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := m.schedule(time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := m.Runs(0)
	if err != nil || len(runs) != 1 || runs[0].JobID != j.ID || runs[0].Trigger != "event" || !strings.Contains(runs[0].Event, "build failed") {
		t.Fatalf("wrong event run: %+v %v", runs, err)
	}
	if err := m.Cancel(runs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := m.schedule(time.Now()); err != nil {
		t.Fatal(err)
	}
	runs, err = m.Runs(0)
	if err != nil || len(runs) != 1 {
		t.Fatal("event consumed more than once")
	}
}
