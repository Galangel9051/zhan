package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/database"
	"zhan/backend/internal/model"
)

func TestTaskPaginationAPI(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, err := auth.New(db, dir, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Init("admin", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := a.Login("admin", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	p := model.Stack{Name: "Fixture", ProjectName: "fixture", Directory: "/fixture", Files: []string{"compose.yaml"}, Desired: "stopped", Interval: 300}
	if err := db.SaveStack(&p, true); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 12; id++ {
		status := "success"
		if id%2 == 0 {
			status = "failed"
		}
		if _, err := db.Exec("INSERT INTO tasks(id,stack_id,task_type,trigger_type,status) VALUES(?,?,'rebuild','manual',?)", id, p.ID, status); err != nil {
			t.Fatal(err)
		}
	}
	router := (&Server{DB: db, Auth: a}).Router()
	call := func(query string, session bool, want int) []byte {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/tasks?"+query, nil)
		if session {
			req.AddCookie(&http.Cookie{Name: "zhan_session", Value: token})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s: got %d, want %d: %s", query, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	call("page=1", false, 401)
	var result struct {
		Data       []model.Task            `json:"data"`
		Pagination database.TaskPagination `json:"pagination"`
	}
	if err := json.Unmarshal(call("page=2&page_size=2&status=failed", true, 200), &result); err != nil {
		t.Fatal(err)
	}
	if result.Pagination.Total != 6 || result.Pagination.TotalPages != 3 || result.Pagination.Page != 2 || len(result.Data) != 2 || result.Data[0].ID != 8 || result.Data[1].ID != 6 {
		t.Fatalf("incorrect filtered page: %+v", result)
	}
	if err := json.Unmarshal(call("page=999&page_size=2&status=failed", true, 200), &result); err != nil || result.Pagination.Page != 3 {
		t.Fatal("page was not clamped")
	}
	for _, query := range []string{"page=", "page=0", "page=-1", "page=abc", "page=1.5", "page=1000000001", "page=1&page_size=0", "page=1&page_size=201", "page=1&page_size=", "page=1&status=invalid", "page=1&stack_id=", "page=1&stack_id=-1", "page=1&before=12"} {
		call(query, true, 400)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(call("limit=3&before=10", true, 200), &legacy); err != nil || legacy["pagination"] != nil {
		t.Fatal("legacy response shape changed")
	}
}
