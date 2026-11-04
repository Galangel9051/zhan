package docker

import (
	"compose-manager/backend/internal/model"
	"compose-manager/backend/internal/testutil"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPathBoundaryAndCommandIdentity(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	project := filepath.Join(root, "project")
	os.Mkdir(project, 0700)
	file := filepath.Join(project, "compose.yaml")
	os.WriteFile(file, []byte("services: {}"), 0600)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	outsideFile := filepath.Join(outside, "compose.yaml")
	os.WriteFile(outsideFile, []byte("services: {}"), 0600)
	os.Symlink(outsideFile, filepath.Join(project, "escape.yaml"))
	fake := &testutil.Fake{Project: "fixture", Running: true}
	c := &Client{Runner: fake, Roots: []string{root}}
	s := model.Stack{Name: "fixture", ProjectName: "fixture", Directory: project, Files: []string{"compose.yaml"}}
	if err := c.Validate(&s); err != nil {
		t.Fatal(err)
	}
	args := Args(s, "stop")
	want := []string{"compose", "-p", "fixture", "--project-directory", project, "-f", file, "stop"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("wrong args: %v", args)
	}
	bad := s
	bad.Files = []string{"escape.yaml"}
	if err := c.Validate(&bad); err == nil {
		t.Fatal("symlink escaped allowed roots")
	}
	if _, err := c.Allowed(root + "-sibling"); err == nil {
		t.Fatal("accepted sibling prefix")
	}
	bad = s
	bad.ProjectName = "--help"
	if err := c.Validate(&bad); err == nil {
		t.Fatal("accepted option injection")
	}
	if err := c.Status(context.Background(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Runtime != "running" || len(s.Services) != 2 {
		t.Fatalf("runtime mismatch: %+v", s)
	}
	for _, svc := range s.Services {
		for _, container := range svc.Containers {
			if strings.ContainsRune(container.Ports, 0) {
				t.Fatal("invalid port metadata")
			}
		}
	}
	if _, err := c.CheckTarget(context.Background(), s, "--help", ""); err == nil {
		t.Fatal("accepted option as service")
	}
	if _, err := c.CheckTarget(context.Background(), s, "", strings.Repeat("c", 64)); err == nil {
		t.Fatal("accepted foreign container")
	}
}

func TestUnrestrictedSelectionAllowsExternalComposeFiles(t *testing.T) {
	project, _ := filepath.EvalSymlinks(t.TempDir())
	other, _ := filepath.EvalSymlinks(t.TempDir())
	file := filepath.Join(other, "override.yaml")
	if err := os.WriteFile(file, []byte("services: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &Client{}
	s := model.Stack{Name: "fixture", ProjectName: "fixture", Directory: project, Files: []string{file}}
	if err := c.Validate(&s); err != nil {
		t.Fatalf("freely selected paths rejected: %v", err)
	}
	c.Roots = []string{project}
	if err := c.Validate(&s); err == nil {
		t.Fatal("explicit administrator restriction ignored")
	}
}
