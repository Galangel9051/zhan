package database

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"zhan/backend/internal/model"
)

func TestTaskEventCatalogSourcesAndDeduplication(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stack := model.Stack{Name: "event-fixture", ProjectName: "event-fixture", Directory: "/fixture", Files: []string{"compose.yaml"}, Desired: "running", Interval: 300}
	if err := db.SaveStack(&stack, true); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ kind, service, container, status, specific string }{
		{"deploy", "", "", "success", "deploy.success"}, {"deploy", "", "", "failed", "deploy.failed"},
		{"rebuild", "", "", "success", "rebuild.success"}, {"rebuild", "", "", "failed", "rebuild.failed"},
		{"git_check", "", "", "success", "git.check.success"}, {"git_check", "", "", "failed", "git.check.failed"},
		{"git_pull", "", "", "success", "git.pull.success"}, {"git_pull", "", "", "failed", "git.pull.failed"},
		{"start", "", "", "success", "project.started"}, {"stop", "", "", "success", "project.stopped"}, {"restart", "", "", "success", "project.restarted"}, {"start", "", "", "failed", "project.action_failed"},
		{"start", "api", "", "success", "service.started"}, {"stop", "api", "", "success", "service.stopped"}, {"restart", "api", "", "success", "service.restarted"}, {"stop", "api", "", "failed", "service.action_failed"},
		{"start", "", "container-id", "success", "container.started"}, {"stop", "", "container-id", "success", "container.stopped"}, {"restart", "", "container-id", "success", "container.restarted"}, {"restart", "", "container-id", "failed", "container.action_failed"},
		{"deploy", "", "", "cancelled", ""},
	}
	for _, tt := range tests {
		t.Run(tt.specific+tt.status, func(t *testing.T) {
			for _, aiID := range []int64{0, 123} {
				if _, err := db.Exec("DELETE FROM ai_events"); err != nil {
					t.Fatal(err)
				}
				task := model.Task{StackID: stack.ID, Type: tt.kind, Service: tt.service, Container: tt.container, Trigger: "manual", AIRunID: aiID}
				if err := db.CreateTask(&task); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Claim(); err != nil {
					t.Fatal(err)
				}
				task.Status = tt.status
				task.Commit = "fixture-commit"
				task.Error = "fixture-error"
				for range 2 {
					if err := db.Finish(task); err != nil {
						t.Fatal(err)
					}
				}
				rows, err := db.Query("SELECT name,payload,stack_id,created_at FROM ai_events ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				names := []string{}
				for rows.Next() {
					var name, payload, created string
					var id int64
					if err := rows.Scan(&name, &payload, &id, &created); err != nil {
						t.Fatal(err)
					}
					var event map[string]any
					if err := json.Unmarshal([]byte(payload), &event); err != nil {
						t.Fatal(err)
					}
					if id != stack.ID || created == "" || event["task_id"] != float64(task.ID) || event["service"] != tt.service || event["container_id"] != tt.container {
						t.Fatalf("wrong payload: %s", payload)
					}
					names = append(names, name)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if aiID != 0 {
					if len(names) != 0 {
						t.Fatal("AI task recursively emitted events")
					}
					continue
				}
				want := "task.queued,task.started,task." + tt.status
				if tt.specific != "" {
					want += "," + tt.specific
				}
				// SQLite does not promise ordering between separate triggers.
				got := map[string]int{}
				for _, name := range names {
					got[name]++
				}
				expected := strings.Split(want, ",")
				if len(names) != len(expected) {
					t.Fatalf("wrong events: %v", names)
				}
				for _, name := range expected {
					if got[name] != 1 {
						t.Fatalf("missing or duplicate %s: %v", name, names)
					}
				}
			}
		})
	}
}

func TestGitUpdatedOnlyForNewRemoteAndWithoutAILoops(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stack := model.Stack{Name: "git-fixture", ProjectName: "git-fixture", Directory: "/fixture", Files: []string{"compose.yaml"}, Desired: "running", Interval: 300}
	if err := db.SaveStack(&stack, true); err != nil {
		t.Fatal(err)
	}
	update := func(remote, local string) {
		t.Helper()
		if _, err := db.Exec("UPDATE deployment_configs SET remote_commit=?,local_commit=? WHERE stack_id=?", remote, local, stack.ID); err != nil {
			t.Fatal(err)
		}
	}
	count := func(want int) {
		t.Helper()
		var got int
		if err := db.QueryRow("SELECT count(*) FROM ai_events WHERE name='git.updated'").Scan(&got); err != nil || got != want {
			t.Fatalf("events=%d want=%d err=%v", got, want, err)
		}
	}
	update("new", "old")
	count(1)
	update("new", "old")
	count(1)
	update("same", "same")
	count(1)
	update("", "old")
	count(1)
	task := model.Task{StackID: stack.ID, Type: "git_check", Trigger: "manual", AIRunID: 123}
	if err := db.CreateTask(&task); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Claim(); err != nil {
		t.Fatal(err)
	}
	update("ai-new", "old")
	count(1)
	task.Status = "success"
	if err := db.Finish(task); err != nil {
		t.Fatal(err)
	}
	update("manual-new", "old")
	count(2)
}
