package database

import (
	"errors"
	"path/filepath"
	"testing"

	"zhan/backend/internal/model"
)

func TestPersistenceConstraintsAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zhan.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	stack := model.Stack{Name: "fixture", ProjectName: "fixture", Directory: "/fixture", Files: []string{"compose.yaml"}, Desired: "running", Interval: 300, Notify: true}
	if err = db.SaveStack(&stack, true); err != nil {
		t.Fatal(err)
	}
	task := model.Task{StackID: stack.ID, Type: "rebuild", Trigger: "manual"}
	if err = db.CreateTask(&task); err != nil {
		t.Fatal(err)
	}
	duplicate := task
	if err = db.CreateTask(&duplicate); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate task was not rejected: %v", err)
	}
	if _, err = db.Claim(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Recover(); err != nil {
		t.Fatal(err)
	}
	saved, err := db.Task(task.ID)
	if err != nil || saved.Status != "cancelled" || saved.Stage != "interrupted" {
		t.Fatalf("recovery failed: %+v %v", saved, err)
	}
	if _, err = db.Stack(stack.ID); err != nil {
		t.Fatal(err)
	}
	var foreign int
	db.QueryRow("PRAGMA foreign_keys").Scan(&foreign)
	if foreign != 1 {
		t.Fatal("foreign keys disabled")
	}
	var mode string
	db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Fatal("WAL disabled")
	}
	if _, err = db.Exec("INSERT INTO tasks(stack_id,task_type,trigger_type,status) VALUES(999,'start','manual','pending')"); err == nil {
		t.Fatal("foreign key constraint missing")
	}
}
