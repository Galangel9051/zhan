package database

import (
	"zhan/backend/internal/model"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "CREATE TABLE IF NOT EXISTS schema_migrations(version TEXT PRIMARY KEY)"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	entries, _ := migrations.ReadDir("migrations")
	for _, entry := range entries {
		var exists int
		if err = db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=?", entry.Name()).Scan(&exists); err != nil {
			db.Close()
			return nil, err
		}
		if exists != 0 {
			continue
		}
		data, _ := migrations.ReadFile("migrations/" + entry.Name())
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, err
		}
		if _, err = tx.Exec(string(data)); err == nil {
			_, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(?)", entry.Name())
		}
		if err != nil {
			tx.Rollback()
			db.Close()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			db.Close()
			return nil, err
		}
	}
	_ = os.Chmod(path, 0600)
	return &Store{db}, nil
}

type scanner interface{ Scan(...any) error }

const stackSelect = `SELECT s.id,s.name,s.project_name,s.project_directory,s.compose_files,s.desired_state,s.created_at,r.url,r.branch,d.polling_enabled,d.poll_interval,d.auto_deploy,d.notify_on_failure,d.no_cache,d.build_pull,d.pull_images,d.remote_commit,d.local_commit,d.last_attempted_commit,d.last_success_commit,d.failure_count,d.failed_commit,d.retry_at,d.next_poll_at FROM compose_stacks s JOIN git_repositories r ON r.stack_id=s.id JOIN deployment_configs d ON d.stack_id=s.id `

func scanStack(row scanner) (model.Stack, error) {
	var s model.Stack
	var files string
	err := row.Scan(&s.ID, &s.Name, &s.ProjectName, &s.Directory, &files, &s.Desired, &s.CreatedAt, &s.Repository, &s.Branch, &s.Polling, &s.Interval, &s.AutoDeploy, &s.Notify, &s.NoCache, &s.BuildPull, &s.PullImages, &s.Remote, &s.Local, &s.Attempted, &s.Success, &s.Failures, &s.FailedCommit, &s.RetryAt, &s.NextPoll)
	if err == nil {
		err = json.Unmarshal([]byte(files), &s.Files)
	}
	s.Runtime = "unknown"
	s.Services = []model.Service{}
	return s, err
}
func (s *Store) Stacks() ([]model.Stack, error) {
	rows, err := s.Query(stackSelect + "ORDER BY s.name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Stack{}
	for rows.Next() {
		item, err := scanStack(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *Store) Stack(id int64) (model.Stack, error) {
	return scanStack(s.QueryRow(stackSelect+"WHERE s.id=?", id))
}
func (s *Store) SaveStack(item *model.Stack, create bool) error {
	files, _ := json.Marshal(item.Files)
	tx, err := s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if create {
		result, err := tx.Exec("INSERT INTO compose_stacks(name,project_name,project_directory,compose_files,desired_state) VALUES(?,?,?,?,?)", item.Name, item.ProjectName, item.Directory, string(files), item.Desired)
		if err != nil {
			return err
		}
		item.ID, _ = result.LastInsertId()
		result, err = tx.Exec("INSERT INTO git_repositories(stack_id,name,url,local_path,branch) VALUES(?,?,?,?,?)", item.ID, item.Name, item.Repository, item.Directory, item.Branch)
		if err != nil {
			return err
		}
		repoID, _ := result.LastInsertId()
		if _, err = tx.Exec("INSERT INTO deployment_configs(stack_id,repository_id) VALUES(?,?)", item.ID, repoID); err != nil {
			return err
		}
	} else {
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM tasks WHERE stack_id=? AND status IN('pending','running')", item.ID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrBusy
		}
		if _, err = tx.Exec("UPDATE compose_stacks SET name=?,project_name=?,project_directory=?,compose_files=? WHERE id=?", item.Name, item.ProjectName, item.Directory, string(files), item.ID); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE git_repositories SET name=?,url=?,local_path=?,branch=? WHERE stack_id=?", item.Name, item.Repository, item.Directory, item.Branch, item.ID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE deployment_configs SET polling_enabled=?,poll_interval=?,auto_deploy=?,notify_on_failure=?,no_cache=?,build_pull=?,pull_images=?,next_poll_at=? WHERE stack_id=?`, item.Polling, item.Interval, item.AutoDeploy, item.Notify, item.NoCache, item.BuildPull, item.PullImages, time.Now().Unix()+int64(item.Interval), item.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Audit(user, action, target, result string) {
	_, _ = s.Exec("INSERT INTO audit_logs(user,action,target,result) VALUES(?,?,?,?)", user, action, target, result)
}
func (s *Store) Audits() ([]model.Audit, error) {
	rows, err := s.Query("SELECT id,user,action,target,result,created_at FROM audit_logs ORDER BY id DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Audit{}
	for rows.Next() {
		var a model.Audit
		if err = rows.Scan(&a.ID, &a.User, &a.Action, &a.Target, &a.Result, &a.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

const taskSelect = `SELECT t.id,t.stack_id,s.name,t.task_type,t.trigger_type,t.service,t.container_id,t.start_after_deploy,t.commit_sha,t.status,t.stage,t.exit_code,t.error_message,t.created_at,t.started_at,t.finished_at,t.cancel_requested FROM tasks t JOIN compose_stacks s ON s.id=t.stack_id `

func scanTask(row scanner) (model.Task, error) {
	var t model.Task
	err := row.Scan(&t.ID, &t.StackID, &t.StackName, &t.Type, &t.Trigger, &t.Service, &t.Container, &t.StartAfter, &t.Commit, &t.Status, &t.Stage, &t.ExitCode, &t.Error, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.CancelRequested)
	return t, err
}
func (s *Store) Task(id int64) (model.Task, error) {
	return scanTask(s.QueryRow(taskSelect+"WHERE t.id=?", id))
}
func (s *Store) Tasks(stack, before int64, limit int) ([]model.Task, error) {
	q := taskSelect + "WHERE 1=1 "
	args := []any{}
	if stack > 0 {
		q += "AND t.stack_id=? "
		args = append(args, stack)
	}
	if before > 0 {
		q += "AND t.id<? "
		args = append(args, before)
	}
	q += "ORDER BY t.id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []model.Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

var ErrBusy = errors.New("该项目已有排队或执行中的任务")

func (s *Store) CreateTask(t *model.Task) error {
	result, err := s.Exec("INSERT INTO tasks(stack_id,task_type,trigger_type,service,container_id,start_after_deploy,status) VALUES(?,?,?,?,?,?,'pending')", t.StackID, t.Type, t.Trigger, t.Service, t.Container, t.StartAfter)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrBusy
		}
		return err
	}
	t.ID, _ = result.LastInsertId()
	return nil
}
func (s *Store) Claim() (model.Task, error) {
	tx, err := s.Begin()
	if err != nil {
		return model.Task{}, err
	}
	defer tx.Rollback()
	t, err := scanTask(tx.QueryRow(taskSelect + "WHERE t.status='pending' ORDER BY t.id LIMIT 1"))
	if err != nil {
		return t, err
	}
	_, err = tx.Exec("UPDATE tasks SET status='running',stage='initializing',started_at=? WHERE id=?", Now(), t.ID)
	if err != nil {
		return t, err
	}
	err = tx.Commit()
	t.Status = "running"
	return t, err
}
func (s *Store) Stage(id int64, stage string) {
	_, _ = s.Exec("UPDATE tasks SET stage=? WHERE id=?", stage, id)
}
func (s *Store) Finish(t model.Task) error {
	_, err := s.Exec("UPDATE tasks SET status=?,stage=?,commit_sha=?,exit_code=?,error_message=?,finished_at=? WHERE id=?", t.Status, t.Stage, t.Commit, t.ExitCode, t.Error, Now(), t.ID)
	return err
}
func (s *Store) Setting(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.Exec("INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(data))
	return err
}
func (s *Store) GetSetting(key string, value any) error {
	var data string
	err := s.QueryRow("SELECT value FROM app_settings WHERE key=?", key).Scan(&data)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(data), value)
}
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Store) Recover() error {
	_, err := s.Exec("UPDATE tasks SET status='cancelled',stage='interrupted',error_message='服务退出，任务已中断；请检查容器状态后手动重试',finished_at=? WHERE status='running'", Now())
	return err
}
func (s *Store) SetDesired(id int64, state string) error {
	_, err := s.Exec("UPDATE compose_stacks SET desired_state=? WHERE id=?", state, id)
	return err
}
