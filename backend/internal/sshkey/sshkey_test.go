package sshkey

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
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
	// Inspect OpenSSH's real option parser without opening a connection.
	output, err := exec.Command("sh", "-c", manager.Command()+" -G git@github.com").CombinedOutput()
	if err != nil {
		t.Fatalf("ssh -G rejected configuration: %s %v", output, err)
	}
	text := string(output)
	if !strings.Contains(text, "identityfile "+filepath.Join(dataDir, "ssh", privateName)) {
		t.Fatalf("incorrect identity path: %s", text)
	}
	if !strings.Contains(text, "userknownhostsfile "+filepath.Join(dataDir, "ssh", knownHostsName)) {
		t.Fatalf("incorrect known_hosts path: %s", text)
	}
	if !strings.Contains(text, "identityagent none") || !strings.Contains(text, "identitiesonly yes") {
		t.Fatal("unexpected inherited identity")
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

func TestPublicKeyAlwaysMatchesPrivateKey(t *testing.T) {
	manager := New(t.TempDir())
	original, _, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	other := New(t.TempDir())
	foreign, _, err := other.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	for _, cached := range []string{foreign.PublicKey, "corrupt-public-key"} {
		if err := os.WriteFile(manager.publicPath(), []byte(cached), 0644); err != nil {
			t.Fatal(err)
		}
		repaired, err := manager.Info()
		if err != nil {
			t.Fatal(err)
		}
		if repaired.PublicKey != original.PublicKey || repaired.Fingerprint != original.Fingerprint {
			t.Fatal("returned unrelated public key")
		}
	}
	if err := os.Remove(manager.publicPath()); err != nil {
		t.Fatal(err)
	}
	if repaired, err := manager.Info(); err != nil || repaired.PublicKey != original.PublicKey {
		t.Fatalf("missing public key not repaired: %v", err)
	}
	if err := os.WriteFile(manager.privatePath(), []byte("corrupt-private-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Info(); err == nil {
		t.Fatal("corrupt private key reported healthy")
	}
}

func TestKeyFailuresDoNotRemoveActiveIdentity(t *testing.T) {
	manager := New(t.TempDir())
	original, _, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(manager.privatePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(manager.publicPath()+".bak", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Regenerate(); err == nil {
		t.Fatal("invalid backup target accepted")
	}
	after, err := os.ReadFile(manager.privatePath())
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("failed rotation removed active key")
	}
	if err := os.Remove(manager.publicPath() + ".bak"); err != nil {
		t.Fatal(err)
	}
	// Stale predictable .tmp files are never reused.
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("unchanged"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, manager.privatePath()+".tmp"); err != nil {
		t.Fatal(err)
	}
	rotated, err := manager.Regenerate()
	if err != nil {
		t.Fatal(err)
	}
	if rotated.PublicKey == original.PublicKey {
		t.Fatal("key was not rotated")
	}
	content, err := os.ReadFile(victim)
	if err != nil || string(content) != "unchanged" {
		t.Fatal("stale temporary link followed")
	}
	backup, err := os.ReadFile(manager.privatePath() + ".bak")
	if err != nil || !bytes.Equal(backup, raw) {
		t.Fatal("backup does not match old key")
	}
	assertMode(t, manager.privatePath()+".bak", 0600)
	parsed, err := ssh.ParsePrivateKey(backup)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed.PublicKey()))) != original.PublicKey {
		t.Fatal("backup is not usable")
	}
}

func TestSSHFilesRejectLinksAndRepairPermissions(t *testing.T) {
	for _, name := range []string{privateName, publicName, knownHostsName, privateName + ".bak"} {
		t.Run(name, func(t *testing.T) {
			manager := New(t.TempDir())
			if _, _, err := manager.Ensure(); err != nil {
				t.Fatal(err)
			}
			victim := filepath.Join(t.TempDir(), "victim")
			if err := os.WriteFile(victim, []byte("unchanged"), 0644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(manager.dir, name)
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, path); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Info(); err == nil {
				t.Fatal("linked key file accepted")
			}
			data, err := os.ReadFile(victim)
			if err != nil || string(data) != "unchanged" {
				t.Fatal("external file modified")
			}
			assertMode(t, victim, 0644)
		})
	}
	manager := New(t.TempDir())
	info, _, err := manager.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{info.KeyPath, info.KnownHostsPath} {
		if err := os.Chmod(path, 0666); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.Info(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, info.KeyPath, 0600)
	assertMode(t, info.KnownHostsPath, 0600)
	if err := os.Remove(info.KeyPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Ensure(); err == nil {
		t.Fatal("silently replaced missing identity")
	}
}

func TestAtomicWriteCleansTemporaryFilesOnFailure(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("target", 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(root, "target", []byte("private"), 0600); err == nil {
		t.Fatal("directory overwritten")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "target" {
		t.Fatal("temporary key leaked")
	}
}

func TestSSHOutputIsBoundedAndRedacted(t *testing.T) {
	manager := New(t.TempDir())
	if _, _, err := manager.Ensure(); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := `#!/bin/sh
printf 'token=private-value\n'
i=0
while [ "$i" -lt 800 ]; do printf '0123456789abcdef'; i=$((i+1)); done
exit 255
`
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Test(t.Context(), "github.com")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || len(result.Output) > 4096 || strings.Contains(result.Output, "private-value") {
		t.Fatal("unbounded or unredacted SSH output")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := manager.Test(ctx, "github.com"); err != nil {
		t.Fatal(err)
	}
}
