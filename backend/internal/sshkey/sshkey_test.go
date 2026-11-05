package sshkey

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCreatesUsableKey(t *testing.T) {
	manager := New(t.TempDir())
	info, created, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected a new key to be generated")
	}
	if !strings.HasPrefix(info.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("unexpected public key: %s", info.PublicKey)
	}
	if !strings.HasPrefix(info.Fingerprint, "SHA256:") {
		t.Fatalf("unexpected fingerprint: %s", info.Fingerprint)
	}
	if info.CreatedAt == "" {
		t.Fatal("created_at is empty")
	}
	if info.KnownHostsCount != 0 {
		t.Fatalf("expected empty known_hosts, got %d", info.KnownHostsCount)
	}
	assertMode(t, filepath.Dir(info.KeyPath), 0700)
	assertMode(t, info.KeyPath, 0600)
	assertMode(t, info.KnownHostsPath, 0600)

	again, created, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("Ensure must not replace an existing key")
	}
	if again.PublicKey != info.PublicKey || again.Fingerprint != info.Fingerprint {
		t.Fatal("Ensure changed the existing key")
	}
}

func TestRegenerateKeepsBackupAndKnownHosts(t *testing.T) {
	manager := New(t.TempDir())
	before, _, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(before.KnownHostsPath, []byte("github.com ssh-ed25519 AAAA\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Regenerate()
	if err != nil {
		t.Fatal(err)
	}
	if after.PublicKey == before.PublicKey {
		t.Fatal("regenerate returned the same public key")
	}
	if _, err = os.Stat(before.KeyPath + ".bak"); err != nil {
		t.Fatalf("missing backup: %v", err)
	}
	if after.KnownHostsCount != 1 {
		t.Fatalf("known_hosts was not preserved: %d", after.KnownHostsCount)
	}
}

func TestResetKnownHosts(t *testing.T) {
	manager := New(t.TempDir())
	info, _, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(info.KnownHostsPath, []byte("github.com ssh-ed25519 AAAA\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = manager.ResetKnownHosts(); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Info()
	if err != nil {
		t.Fatal(err)
	}
	if after.KnownHostsCount != 0 {
		t.Fatalf("known_hosts not cleared: %d", after.KnownHostsCount)
	}
}

func TestCommandQuotesPaths(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data with space")
	manager := New(dataDir)
	if _, _, err := manager.Ensure(); err != nil {
		t.Fatal(err)
	}
	command := manager.Command()
	if !strings.Contains(command, "StrictHostKeyChecking=accept-new") {
		t.Fatalf("missing host key policy: %s", command)
	}
	if !strings.Contains(command, "-i '"+filepath.Join(dataDir, "ssh", "id_ed25519")+"'") {
		t.Fatalf("private key path is not quoted: %s", command)
	}
	if !strings.Contains(command, "UserKnownHostsFile='"+filepath.Join(dataDir, "ssh", "known_hosts")+"'") {
		t.Fatalf("known_hosts path is not quoted: %s", command)
	}
}

func TestValidHost(t *testing.T) {
	for _, host := range []string{"github.com", "gitlab.com", "git.example.internal"} {
		if !ValidHost(host) {
			t.Fatalf("expected %q to be valid", host)
		}
	}
	for _, host := range []string{"", "-oProxyCommand=id", "github.com;id", "github.com/../x", "github.com ", strings.Repeat("a", 254)} {
		if ValidHost(host) {
			t.Fatalf("expected %q to be rejected", host)
		}
	}
}

func TestTestSuccessAndPermissionDenied(t *testing.T) {
	manager := New(t.TempDir())
	if _, _, err := manager.Ensure(); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\n"+body+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	script(`echo "Hi test! You've successfully authenticated, but GitHub does not provide shell access."; exit 1`)
	result, err := manager.Test(context.Background(), "github.com")
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || !strings.Contains(result.Message, "成功") {
		t.Fatalf("unexpected success result: %+v", result)
	}

	script(`echo "git@github.com: Permission denied (publickey)."; exit 255`)
	result, err = manager.Test(context.Background(), "github.com")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatalf("expected permission failure: %+v", result)
	}
	if !strings.Contains(result.Hint, "Deploy Keys") {
		t.Fatalf("missing actionable hint: %s", result.Hint)
	}
}

func TestTestRejectsInvalidHost(t *testing.T) {
	manager := New(t.TempDir())
	if _, _, err := manager.Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Test(context.Background(), "-oProxyCommand=id"); err == nil {
		t.Fatal("expected invalid host to be rejected")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := stat.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
