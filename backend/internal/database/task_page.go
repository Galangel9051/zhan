package database

import (
	"errors"

	"zhan/backend/internal/model"
)

type TaskPagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// Count and read from one snapshot so the last page stays valid while tasks
// change. Cursor-based Tasks remains available for existing API consumers.
func (s *Store) TaskPage(stack int64, status string, page, size int) ([]model.Task, TaskPagination, error) {
	pagination := TaskPagination{Page: page, PageSize: size}
	if page < 1 || size < 1 || size > 200 {
		return nil, pagination, errors.New("无效分页参数")
	}
	where := "WHERE 1=1"
	args := []any{}
	if stack > 0 {
		where += " AND t.stack_id=?"
		args = append(args, stack)
	}
	if status != "" && status != "all" {
		where += " AND t.status=?"
		args = append(args, status)
	}
	tx, err := s.Begin()
	if err != nil {
		return nil, pagination, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow("SELECT count(*) FROM tasks t "+where, args...).Scan(&pagination.Total); err != nil {
		return nil, pagination, err
	}
	pagination.TotalPages = max(1, (pagination.Total+size-1)/size)
	pagination.Page = min(page, pagination.TotalPages)
	args = append(args, size, (pagination.Page-1)*size)
	rows, err := tx.Query(taskSelect+where+" ORDER BY t.id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, pagination, err
	}
	defer rows.Close()
	items := []model.Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, pagination, err
		}
		items = append(items, task)
	}
	if err := rows.Err(); err != nil {
		return nil, pagination, err
	}
	return items, pagination, nil
}
