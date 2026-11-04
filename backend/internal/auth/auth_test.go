package auth

import (
	"compose-manager/backend/internal/database"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminSessionAndEncryption(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	password := "correct horse battery staple"
	if err = a.Init("admin", password); err != nil {
		t.Fatal(err)
	}
	if err = a.Init("other", password); err == nil {
		t.Fatal("initialization overwrote existing administrator")
	}
	var hash string
	if err = db.QueryRow("SELECT password_hash FROM users").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, password) || !Verify(hash, password) || Verify(hash, "wrong") {
		t.Fatal("invalid password hashing")
	}
	if _, _, err = a.Login("admin", "incorrect"); err == nil {
		t.Fatal("accepted wrong password")
	}
	token, u, err := a.Login("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.CSRF) != 64 {
		t.Fatal("missing csrf token")
	}
	if _, err = a.Session(token); err != nil {
		t.Fatal(err)
	}
	encrypted, err := a.Encrypt("smtp-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "smtp-secret") {
		t.Fatal("plaintext credential")
	}
	a2, err := New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	value, err := a2.Decrypt(encrypted)
	if err != nil || value != "smtp-secret" {
		t.Fatal("key did not survive reinitialization")
	}
	a.Logout(token)
	if _, err = a.Session(token); err == nil {
		t.Fatal("logout failed")
	}
}

func TestDefaultPasswordRequiresChangeAndIsNotRecreated(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.InitDefault("admin")
	if err != nil || !created {
		t.Fatalf("bootstrap: %v %v", created, err)
	}
	first, u, err := a.Login("admin", "123456")
	if err != nil || !u.MustChangePassword {
		t.Fatalf("default login not restricted: %+v %v", u, err)
	}
	second, _, err := a.Login("admin", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.ChangePassword(u.ID, "wrong", "abcdef"); err == nil {
		t.Fatal("accepted wrong current password")
	}
	if err = a.ChangePassword(u.ID, "123456", "123456"); err == nil {
		t.Fatal("allowed default password reuse")
	}
	if err = a.ChangePassword(u.ID, "123456", "short"); err == nil {
		t.Fatal("accepted weak new password")
	}
	if err = a.ChangePassword(u.ID, "123456", "abcdef"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		if _, err = a.Session(token); err == nil {
			t.Fatal("old session survived password change")
		}
	}
	if _, _, err = a.Login("admin", "123456"); err == nil {
		t.Fatal("default password still works")
	}
	_, u, err = a.Login("admin", "abcdef")
	if err != nil || u.MustChangePassword {
		t.Fatalf("new password not accepted: %+v %v", u, err)
	}
	created, err = a.InitDefault("admin")
	if err != nil || created {
		t.Fatal("bootstrap overwrote existing admin")
	}
	if _, _, err = a.Login("admin", "abcdef"); err != nil {
		t.Fatal("startup reset password")
	}
}

func TestPasswordMinimumSixCharacters(t *testing.T) {
	for _, password := range []string{"abcde", "中文密码五"} {
		if _, err := Hash(password); err == nil {
			t.Fatalf("accepted fewer than six characters: %q", password)
		}
	}
	for _, password := range []string{"abcdef", "新的六位密码"} {
		hash, err := Hash(password)
		if err != nil || !Verify(hash, password) {
			t.Fatalf("six-character password rejected: %v", err)
		}
	}
}
