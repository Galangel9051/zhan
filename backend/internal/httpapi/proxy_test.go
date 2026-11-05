package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/command"
	"zhan/backend/internal/database"
	"zhan/backend/internal/proxy"
)

func TestProxyAuthenticationCSRFAndRevision(t *testing.T) {
	dir := t.TempDir()
	db, e := database.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a, e := auth.New(db, dir, 12)
	if e != nil {
		t.Fatal(e)
	}
	a.Init("admin", "test-password")
	token, u, e := a.Login("admin", "test-password")
	if e != nil {
		t.Fatal(e)
	}
	m := proxy.New(db, a, command.OSRunner{}, dir)
	if e = m.Start(); e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	router := (&Server{DB: db, Auth: a, Proxy: m}).Router()
	call := func(method, path string, payload any, session, csrf string, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(method, "/api/v1/proxy"+path, bytes.NewReader(b))
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "zhan_session", Value: session})
		}
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	call("GET", "", nil, "", "", 401)
	c := proxy.Defaults()
	call("PUT", "/config", c, token, "", 403)
	call("PUT", "/config", c, token, u.CSRF, 200)
	call("PUT", "/config", c, token, u.CSRF, 400)
	var state struct{ Data proxy.State }
	if e = json.Unmarshal(call("GET", "", nil, token, "", 200), &state); e != nil {
		t.Fatal(e)
	}
	if state.Data.Revision != 1 {
		t.Fatal("save was not persisted")
	}
	// First-login sessions cannot access proxy operations either.
	db.Exec("UPDATE users SET must_change_password=1")
	call("GET", "", nil, token, "", 403)
}
