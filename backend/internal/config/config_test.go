package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnrestrictedDefaultAndExplicitRoots(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("CM_ALLOWED_ROOTS", "")
	t.Setenv("CM_LISTEN", "")
	t.Setenv("CM_DATA_DIR", "")
	cfg, err := Load("")
	if err != nil || len(cfg.AllowedRoots) != 0 {
		t.Fatalf("default should allow any accessible directory: %v %v", cfg.AllowedRoots, err)
	}
	path := filepath.Join(root, "config.yaml")
	os.WriteFile(path, []byte("allowed_roots:\n  - "+root+"\n"), 0600)
	cfg, err = Load(path)
	if err != nil || len(cfg.AllowedRoots) != 1 || cfg.AllowedRoots[0] != root {
		t.Fatalf("explicit roots ignored: %v %v", cfg.AllowedRoots, err)
	}
	t.Setenv("CM_ALLOWED_ROOTS", string(filepath.Separator))
	cfg, err = Load(path)
	if err != nil || len(cfg.AllowedRoots) != 1 || cfg.AllowedRoots[0] != string(filepath.Separator) {
		t.Fatalf("explicit filesystem root rejected: %v %v", cfg.AllowedRoots, err)
	}
	t.Setenv("CM_ALLOWED_ROOTS", "")
	os.WriteFile(path, []byte("allowed_roots: []\n"), 0600)
	cfg, err = Load(path)
	if err != nil || len(cfg.AllowedRoots) != 0 {
		t.Fatalf("empty roots should be unrestricted: %v", err)
	}
}
