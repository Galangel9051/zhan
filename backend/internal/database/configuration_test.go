package database

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"zhan/backend/internal/model"
)

func TestConfigurationRestoreIsAtomicAndPreservesHistory(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	project := model.Stack{Name: "Original", ProjectName: "existing", Directory: "/original", Files: []string{"compose.yaml"}, Desired: "running", Interval: 300}
	other := model.Stack{Name: "Keep", ProjectName: "unrelated", Directory: "/keep", Files: []string{"compose.yaml"}, Desired: "stopped", Interval: 300}
	for _, p := range []*model.Stack{&project, &other} {
		if err := db.SaveStack(p, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Setting("log_retention_days", 30); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO credentials(name,ciphertext) VALUES('smtp_password','existing-ciphertext')"); err != nil {
		t.Fatal(err)
	}
	finished := model.Task{StackID: project.ID, Type: "rebuild", Trigger: "manual"}
	if err := db.CreateTask(&finished); err != nil {
		t.Fatal(err)
	}
	finished.Status, finished.Stage = "success", "completed"
	if err := db.Finish(finished); err != nil {
		t.Fatal(err)
	}
	revision := func() string {
		t.Helper()
		v, err := db.ConfigurationRevision(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	original := revision()
	if original != revision() {
		t.Fatal("revision is unstable")
	}
	updated := project
	updated.Name, updated.Directory, updated.Polling, updated.AutoDeploy = "Restored", "/restored", true, true
	created := model.Stack{Name: "New", ProjectName: "new", Directory: "/new", Files: []string{"compose.yaml"}, Interval: 300, Polling: true, AutoDeploy: true}
	projects := []model.Stack{created, updated}
	settings := map[string]json.RawMessage{"log_retention_days": json.RawMessage("60")}
	assertUnchanged := func() {
		t.Helper()
		if revision() != original {
			t.Fatal("failed restore changed configuration")
		}
		var count int
		if err := db.QueryRow("SELECT count(*) FROM compose_stacks WHERE project_name='new'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial project import: %d (%v)", count, err)
		}
	}
	if err := db.RestoreConfiguration(ctx, projects, settings, "stale", false); !errors.Is(err, ErrConfigurationChanged) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	assertUnchanged()
	active := model.Task{StackID: project.ID, Type: "rebuild", Trigger: "manual"}
	if err := db.CreateTask(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreConfiguration(ctx, projects, settings, original, false); !errors.Is(err, ErrBusy) {
		t.Fatalf("active project accepted: %v", err)
	}
	assertUnchanged()
	if _, err := db.Exec("DELETE FROM tasks WHERE id=?", active.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TRIGGER reject_import BEFORE INSERT ON app_settings WHEN NEW.key='log_retention_days' BEGIN SELECT RAISE(ABORT,'fixture failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreConfiguration(ctx, projects, settings, original, false); err == nil {
		t.Fatal("failed write accepted")
	}
	assertUnchanged()
	if _, err := db.Exec("DROP TRIGGER reject_import"); err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreConfiguration(ctx, projects, settings, original, false); err != nil {
		t.Fatal(err)
	}
	got, err := db.Stack(project.ID)
	if err != nil || got.Name != "Restored" || got.Directory != "/restored" || got.Desired != "running" || got.Polling || got.AutoDeploy {
		t.Fatalf("incorrect restored project: %+v (%v)", got, err)
	}
	keep, err := db.Stack(other.ID)
	if err != nil || keep.Name != "Keep" || keep.Directory != "/keep" {
		t.Fatal("unrelated project changed")
	}
	items, err := db.Stacks()
	if err != nil || len(items) != 3 {
		t.Fatalf("incorrect imported project count: %d (%v)", len(items), err)
	}
	for _, item := range items {
		if item.ProjectName == "new" && (item.Desired != "stopped" || item.Polling || item.AutoDeploy) {
			t.Fatal("new project automation was enabled")
		}
	}
	if history, err := db.Task(finished.ID); err != nil || history.Status != "success" {
		t.Fatal("task history was lost")
	}
	for _, kind := range []string{"git_check", "deploy"} {
		if err := db.CreateTask(&model.Task{StackID: project.ID, Type: kind, Trigger: "scheduled"}); err == nil {
			t.Fatal("scheduler queued work from a stale configuration")
		}
	}
	var credential string
	if err := db.QueryRow("SELECT ciphertext FROM credentials WHERE name='smtp_password'").Scan(&credential); err != nil || credential != "existing-ciphertext" {
		t.Fatal("local credential was not preserved")
	}
	if err := db.RestoreConfiguration(ctx, nil, settings, revision(), true); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM credentials WHERE name='smtp_password'").Scan(&count); err != nil || count != 0 {
		t.Fatal("credential for a changed SMTP connection was retained")
	}
}
