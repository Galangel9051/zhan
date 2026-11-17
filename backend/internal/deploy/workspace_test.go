package deploy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zhan/backend/internal/config"
)

func TestWorkspaceGateOverlapsAndIndependentProjects(t *testing.T) {
	e := New(nil, nil, config.Config{}, nil)
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	release := make(chan struct{})
	entered := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = e.WithWorkspace(context.Background(), parent, func() error { close(entered); <-release; return nil })
		close(done)
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := e.WithWorkspace(ctx, other, func() error { return nil }); err != nil {
		t.Fatal("independent project blocked", err)
	}
	ran := false
	if err := e.WithWorkspace(ctx, child, func() error { ran = true; return nil }); err == nil || ran {
		t.Fatal("overlapping workspace was not blocked")
	}
	close(release)
	<-done
	if err := e.WithWorkspace(context.Background(), child, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
