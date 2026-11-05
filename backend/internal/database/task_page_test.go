package database

import (
	"path/filepath"
	"testing"

	"zhan/backend/internal/model"
)

func TestTaskPageCountsFiltersAndBounds(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"first", "second"} {
		p := model.Stack{Name: name, ProjectName: name, Directory: "/fixture", Files: []string{"compose.yaml"}, Desired: "stopped", Interval: 300}
		if err := db.SaveStack(&p, true); err != nil {
			t.Fatal(err)
		}
	}
	for id := 1; id <= 123; id++ {
		stack, status := 1, "success"
		if id <= 120 && id%2 == 0 {
			status = "failed"
		}
		if id == 121 || id == 123 {
			stack = 2
		}
		if id == 122 {
			status = "pending"
		}
		if id == 123 {
			status = "running"
		}
		if _, err := db.Exec("INSERT INTO tasks(id,stack_id,task_type,trigger_type,status) VALUES(?,?,'rebuild','manual',?)", id, stack, status); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name                   string
		stack                  int64
		status                 string
		page, size             int
		wantPage, total, pages int
		count, first, last     int
	}{
		{"first", 0, "all", 1, 50, 1, 123, 3, 50, 123, 74},
		{"last", 0, "all", 3, 50, 3, 123, 3, 23, 23, 1},
		{"clamped", 0, "all", 999, 50, 3, 123, 3, 23, 23, 1},
		{"global status", 0, "failed", 2, 50, 2, 60, 2, 10, 20, 2},
		{"project", 2, "all", 2, 1, 2, 2, 2, 1, 121, 121},
		{"combined filters", 2, "running", 1, 50, 1, 1, 1, 1, 123, 123},
		{"empty", 0, "cancelled", 99, 50, 1, 0, 1, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, info, err := db.TaskPage(tc.stack, tc.status, tc.page, tc.size)
			if err != nil {
				t.Fatal(err)
			}
			if info.Page != tc.wantPage || info.Total != tc.total || info.TotalPages != tc.pages || info.PageSize != tc.size || len(items) != tc.count || items == nil {
				t.Fatalf("incorrect page: %+v, %d items", info, len(items))
			}
			if len(items) > 0 && (items[0].ID != int64(tc.first) || items[len(items)-1].ID != int64(tc.last)) {
				t.Fatalf("incorrect task ordering: %d ... %d", items[0].ID, items[len(items)-1].ID)
			}
		})
	}
	for _, input := range [][2]int{{0, 50}, {1, 0}, {1, 201}} {
		if _, _, err := db.TaskPage(0, "all", input[0], input[1]); err == nil {
			t.Fatal("invalid page parameters accepted")
		}
	}
	legacy, err := db.Tasks(0, 100, 20)
	if err != nil || len(legacy) != 20 || legacy[0].ID != 99 || legacy[19].ID != 80 {
		t.Fatal("legacy cursor pagination changed")
	}
}
