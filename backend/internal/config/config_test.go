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
	t.Setenv("ZHAN_ALLOWED_ROOTS", "")
	t.Setenv("ZHAN_LISTEN", "")
	t.Setenv("ZHAN_DATA_DIR", "")
	t.Setenv("ZHAN_HOST_DATA_DIR", "")
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
	t.Setenv("ZHAN_ALLOWED_ROOTS", string(filepath.Separator))
	cfg, err = Load(path)
	if err != nil || len(cfg.AllowedRoots) != 1 || cfg.AllowedRoots[0] != string(filepath.Separator) {
		t.Fatalf("explicit filesystem root rejected: %v %v", cfg.AllowedRoots, err)
	}
	t.Setenv("ZHAN_ALLOWED_ROOTS", "")
	os.WriteFile(path, []byte("allowed_roots: []\n"), 0600)
	cfg, err = Load(path)
	if err != nil || len(cfg.AllowedRoots) != 0 {
		t.Fatalf("empty roots should be unrestricted: %v", err)
	}
}

func TestDataDirectoryIndependentOfWorkingDirectory(t *testing.T) {
	t.Setenv("ZHAN_DATA_DIR", "")
	t.Setenv("ZHAN_HOST_DATA_DIR", "")
	t.Setenv("ZHAN_ALLOWED_ROOTS", "")
	t.Setenv("ZHAN_LISTEN", "")
	first := t.TempDir()
	second := t.TempDir()
	path := filepath.Join(first, "config.yaml")
	if err := os.WriteFile(path, []byte("data_dir: data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, custom := range []string{"", "data-custom", filepath.Join(first, "absolute")} {
		t.Setenv("ZHAN_DATA_DIR", custom)
		t.Chdir(first)
		before, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		t.Chdir(second)
		after, err := Load("")
		if err != nil || before.DataDir != after.DataDir || !filepath.IsAbs(after.DataDir) {
			t.Fatalf("cwd changed data directory: %s -> %s (%v)", before.DataDir, after.DataDir, err)
		}
		if custom == "" {
			withConfig, err := Load(path)
			if err != nil || withConfig.DataDir != before.DataDir {
				t.Fatalf("example relative config must use the same data: %s (%v)", withConfig.DataDir, err)
			}
		}
	}
	shared := filepath.Join(first, "shared")
	t.Setenv("ZHAN_DATA_DIR", "")
	t.Setenv("ZHAN_HOST_DATA_DIR", shared)
	cfg, err := Load("")
	if err != nil || cfg.DataDir != shared {
		t.Fatalf("Docker override not shared with native launch: %s (%v)", cfg.DataDir, err)
	}
	t.Setenv("ZHAN_DATA_DIR", shared)
	if _, err := Load(""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZHAN_DATA_DIR", filepath.Join(first, "other"))
	if _, err := Load(""); err == nil {
		t.Fatal("conflicting Docker and native directories should fail")
	}
}

func TestResolveDataBase(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "backend"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backend", "go.mod"), []byte("module fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, executable, source, want string }{
		{"source run", "/tmp/go-build/exe/server", filepath.Join(root, "backend/internal/config/config.go"), root},
		{"repository binary", filepath.Join(root, "bin/zhan"), "zhan/backend/internal/config/config.go", root},
		{"installed binary", "/opt/zhan/zhan", "zhan/backend/internal/config/config.go", "/opt/zhan"},
		{"missing source checkout", "/opt/zhan/zhan", "/missing/backend/internal/config/config.go", "/opt/zhan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDataBase(tc.executable, tc.source); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}
