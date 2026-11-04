package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"compose-manager/backend/internal/auth"
	"compose-manager/backend/internal/config"
	"compose-manager/backend/internal/database"
	"compose-manager/backend/internal/docker"
)

type browserResponse struct {
	Data struct {
		Path       string      `json:"path"`
		Parent     string      `json:"parent"`
		Entries    []fileEntry `json:"entries"`
		Next       int         `json:"next_offset"`
		More       bool        `json:"has_more"`
		Suggested  []string    `json:"suggested_files"`
		Restricted bool        `json:"restricted"`
	}
}

func TestFilesystemBrowserPermissionsPaginationAndMetadata(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	for _, name := range []string{"alpha", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"compose.yaml", "override.yml", "metadata.json", "readme.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("secret file content never exposed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	db, err := database.Open(filepath.Join(data, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, data, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Init("admin", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := a.Login("admin", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	d := &docker.Client{}
	server := httptest.NewServer((&Server{DB: db, Auth: a, Docker: d, Config: config.Config{SessionHours: 12}}).Router())
	defer server.Close()
	call := func(path string, want int, authenticated bool) browserResponse {
		t.Helper()
		req, _ := http.NewRequest("GET", server.URL+"/api/v1/filesystem"+path, nil)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "cm_session", Value: token})
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("browser status %d expected %d: %s", response.StatusCode, want, body)
		}
		if strings.Contains(string(body), "secret file content never exposed") {
			t.Fatal("file contents leaked")
		}
		var result browserResponse
		if want == 200 {
			if err = json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	query := "?path=" + url.QueryEscape(root)
	call(query, 401, false)
	result := call(query, 200, true)
	if result.Data.Restricted || result.Data.Path != root || result.Data.Parent != filepath.Dir(root) {
		t.Fatalf("unexpected unrestricted result: %+v", result.Data)
	}
	names := []string{}
	for _, item := range result.Data.Entries {
		names = append(names, item.Name)
	}
	if len(names) != 5 || strings.Contains(strings.Join(names, ","), "readme.txt") || strings.Contains(strings.Join(names, ","), ".hidden") {
		t.Fatalf("bad entries: %v", names)
	}
	if len(result.Data.Suggested) != 1 || result.Data.Suggested[0] != filepath.Join(root, "compose.yaml") {
		t.Fatal("Compose auto-detection failed")
	}
	result = call(query+"&hidden=1", 200, true)
	if len(result.Data.Entries) != 6 {
		t.Fatal("hidden directory missing")
	}
	seen := map[string]bool{}
	offset := 0
	for page := 0; page < 10; page++ {
		result = call(query+"&limit=1&offset="+strconv.Itoa(offset), 200, true)
		for _, item := range result.Data.Entries {
			if seen[item.Path] {
				t.Fatal("pagination duplicated entry")
			}
			seen[item.Path] = true
		}
		if !result.Data.More {
			break
		}
		if result.Data.Next <= offset {
			t.Fatal("pagination made no progress")
		}
		offset = result.Data.Next
	}
	if len(seen) != 5 {
		t.Fatalf("pagination omitted entries: %v", seen)
	}
	result = call("?path="+url.QueryEscape(filepath.Join(root, "linked")), 200, true)
	if result.Data.Path != outside {
		t.Fatal("symlink navigation failed")
	}
	call("?path="+url.QueryEscape(filepath.Join(root, "compose.yaml")), 400, true)
	call("?path="+url.QueryEscape(filepath.Join(root, "missing")), 400, true)
	d.Roots = []string{root}
	result = call(query, 200, true)
	if !result.Data.Restricted || result.Data.Parent != "" {
		t.Fatal("explicit root boundary ignored")
	}
	for _, item := range result.Data.Entries {
		if item.Name == "linked" {
			t.Fatal("restricted symlink escaped boundary")
		}
	}
	call("?path="+url.QueryEscape(outside), 400, true)
}
