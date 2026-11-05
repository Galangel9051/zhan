package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/config"
	"zhan/backend/internal/database"
	"zhan/backend/internal/sshkey"
)

func TestGitCredentialSettings(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "zhan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	a.Init("admin", "correct-password-1234")
	keys := sshkey.New(dir)
	original, _, err := keys.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&Server{DB: db, Auth: a, Git: keys, Config: config.Config{DataDir: dir, SessionHours: 12}}).Router())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	call := func(method, path string, payload any, want int, token string) []byte {
		t.Helper()
		var body io.Reader
		if payload != nil {
			data, _ := json.Marshal(payload)
			body = bytes.NewReader(data)
		}
		req, _ := http.NewRequest(method, server.URL+"/api/v1"+path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.StatusCode, want, data)
		}
		return data
	}

	call("GET", "/settings/git", nil, 401, "")
	data := call("POST", "/auth/login", map[string]string{"username": "admin", "password": "correct-password-1234"}, 200, "")
	var session struct{ Data auth.User }
	if err = json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	csrf := session.Data.CSRF
	if csrf == "" {
		t.Fatal("missing CSRF token")
	}

	data = call("GET", "/settings/git", nil, 200, "")
	var info struct{ Data sshkey.Info }
	if err = json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Data.PublicKey != original.PublicKey || info.Data.Fingerprint != original.Fingerprint {
		t.Fatalf("unexpected key info: %+v", info.Data)
	}
	if strings.Contains(string(data), "PRIVATE KEY") || strings.Contains(strings.ToLower(string(data)), "private_key") {
		t.Fatalf("private key leaked: %s", data)
	}

	call("POST", "/settings/git/regenerate", map[string]bool{"confirm": true}, 403, "")
	call("POST", "/settings/git/regenerate", map[string]bool{"confirm": false}, 400, csrf)
	data = call("POST", "/settings/git/regenerate", map[string]bool{"confirm": true}, 200, csrf)
	var regenerated struct{ Data sshkey.Info }
	if err = json.Unmarshal(data, &regenerated); err != nil {
		t.Fatal(err)
	}
	if regenerated.Data.PublicKey == original.PublicKey {
		t.Fatal("regenerate did not replace the public key")
	}

	call("POST", "/settings/git/test", map[string]string{"host": "-oProxyCommand=id"}, 400, csrf)
	call("POST", "/settings/git/known-hosts/reset", map[string]bool{"confirm": true}, 200, csrf)
	after, err := keys.Info()
	if err != nil {
		t.Fatal(err)
	}
	if after.KnownHostsCount != 0 {
		t.Fatalf("known hosts not cleared: %d", after.KnownHostsCount)
	}
}
