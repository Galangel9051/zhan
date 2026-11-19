package aiops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata"

	"zhan/backend/internal/database"
)

func (m *Manager) validate(j *Job) error {
	if j.Engine != "" && j.Engine != "native" && j.Engine != "pi" {
		return errors.New("未知 AI 执行引擎")
	}
	if j.AllowCommands && j.Engine != "pi" {
		return errors.New("命令工具需要 Pi 引擎")
	}
	if j.TestImage == "" {
		j.TestImage = "python:3.13-alpine"
	}
	if strings.ContainsAny(j.TestImage, " \t\r\n") || strings.HasPrefix(j.TestImage, "-") {
		return errors.New("无效测试镜像")
	}
	j.Name = strings.TrimSpace(j.Name)
	if j.Model != "" {
		s, err := m.Settings(false)
		if err != nil {
			return err
		}
		if _, err = s.selectedModel(j.Model); err != nil {
			return err
		}
	}
	if j.Name == "" || len(j.Name) > 128 || strings.TrimSpace(j.Prompt) == "" || len(j.Prompt) > 16000 {
		return errors.New("请填写任务名称及指令（最多 16000 字节）")
	}
	if j.Timeout < 10 || j.Timeout > 1800 || j.MaxSteps < 1 || j.MaxSteps > 50 {
		return errors.New("超时须为 10–1800 秒，工具步数须为 1–50")
	}
	path, err := m.Docker.Allowed(j.Workspace)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("工作区必须是目录")
	}
	j.Workspace = path
	if j.StackID < 0 {
		return errors.New("无效项目")
	}
	if j.StackID > 0 {
		stack, err := m.DB.Stack(j.StackID)
		if err != nil {
			return errors.New("关联项目不存在")
		}
		// Actions operate on the selected stack; bind its workspace to prevent accidental cross-project operations.
		dir, err := m.Docker.Allowed(stack.Directory)
		if err != nil {
			return err
		}
		if j.AllowActions && dir != path {
			return errors.New("允许项目操作时，工作区必须等于关联项目目录")
		}
	}
	if j.AllowActions && j.StackID == 0 {
		return errors.New("请先关联项目再授权项目操作")
	}
	switch j.Schedule {
	case "manual":
	case "interval":
		if j.Interval < 60 || j.Interval > 2592000 {
			return errors.New("执行间隔须为 60–2592000 秒")
		}
	case "daily":
		if _, err := time.Parse("15:04", j.DailyAt); err != nil {
			return errors.New("每天执行时间须为 HH:MM")
		}
		if _, err := time.LoadLocation(j.Timezone); err != nil {
			return errors.New("无效时区")
		}
	case "event":
		if !validEvent(j.EventName) {
			return errors.New("请选择内置事件或填写有效自定义事件名（字母、数字、点、下划线或连字符）")
		}
	default:
		return errors.New("无效触发方式")
	}
	// Do not permit credentials or platform state to be exposed through a workspace containing them.
	data, err := filepath.Abs(m.Engine.Config.DataDir)
	if err != nil {
		return err
	}
	if real, e := filepath.EvalSymlinks(data); e == nil {
		data = real
	}
	rel, err := filepath.Rel(path, data)
	if err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return errors.New("工作区不能包含栈长数据目录")
	}
	rel, err = filepath.Rel(data, path)
	if err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return errors.New("工作区不能位于栈长数据目录中")
	}
	return nil
}
func (m *Manager) SaveJob(j *Job) error {
	if err := m.validate(j); err != nil {
		return err
	}
	tx, err := m.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cursor int64
	if err = tx.QueryRow("SELECT coalesce(max(id),0) FROM ai_events").Scan(&cursor); err != nil {
		return err
	}
	j.NextRun = j.next(time.Now())
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if j.ID == 0 {
		res, e := tx.Exec("INSERT INTO ai_jobs(config,enabled,next_run,event_cursor) VALUES(?,?,?,?)", string(data), j.Enabled, j.NextRun, cursor)
		if e != nil {
			return e
		}
		j.ID, err = res.LastInsertId()
	} else {
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM ai_runs WHERE job_id=? AND status IN ('pending','running')", j.ID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errors.New("任务正在排队或执行，请先取消后修改")
		}
		res, e := tx.Exec("UPDATE ai_jobs SET config=?,enabled=?,next_run=?,event_cursor=? WHERE id=?", string(data), j.Enabled, j.NextRun, cursor, j.ID)
		if e != nil {
			return e
		}
		affected, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if affected == 0 {
			return sql.ErrNoRows
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (m *Manager) Jobs() ([]Job, error) {
	rows, err := m.DB.Query("SELECT id,config,enabled,next_run FROM ai_jobs ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		var j Job
		var data string
		var id, next int64
		var enabled bool
		if err = rows.Scan(&id, &data, &enabled, &next); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &j); err != nil {
			return nil, err
		}
		j.ID = id
		j.NextRun = next
		j.Enabled = enabled
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
func (m *Manager) Enqueue(id int64) (int64, error) {
	s, err := m.Settings(false)
	if err != nil {
		return 0, err
	}
	if !s.Enabled {
		return 0, errors.New("请先启用 AI 接口")
	}
	tx, err := m.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var data string
	if err = tx.QueryRow("SELECT config FROM ai_jobs WHERE id=?", id).Scan(&data); err != nil {
		return 0, err
	}
	runID, err := insertRun(tx, id, data, "manual", "")
	if err != nil {
		return 0, err
	}
	return runID, tx.Commit()
}
func insertRun(tx *sql.Tx, id int64, config, trigger, event string) (int64, error) {
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM ai_runs WHERE status IN ('pending','running')").Scan(&count); err != nil {
		return 0, err
	}
	if count >= 100 {
		return 0, errors.New("AI 队列已满")
	}
	res, err := tx.Exec("INSERT INTO ai_runs(job_id,config,trigger_type,event,created_at) VALUES(?,?,?,?,?)", id, config, trigger, event, database.Now())
	if err != nil {
		return 0, fmt.Errorf("任务已排队或无法创建：%w", err)
	}
	return res.LastInsertId()
}

const runSelect = "SELECT id,job_id,config,trigger_type,event,status,output,error,created_at,started_at,finished_at FROM ai_runs "

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var data string
	err := row.Scan(&r.ID, &r.JobID, &data, &r.Trigger, &r.Event, &r.Status, &r.Output, &r.Error, &r.Created, &r.Started, &r.Finished)
	if err == nil {
		err = json.Unmarshal([]byte(data), &r.Config)
		r.Config.ID = r.JobID
	}
	return r, err
}
func (m *Manager) Run(id int64) (Run, error) {
	return scanRun(m.DB.QueryRow(runSelect+"WHERE id=?", id))
}
func (m *Manager) Runs(before int64) ([]Run, error) {
	rows, err := m.DB.Query(strings.Replace(runSelect, ",output,", ",'' AS output,", 1)+"WHERE (?=0 OR id<?) ORDER BY id DESC LIMIT 50", before, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		r, e := scanRun(rows)
		if e != nil {
			return nil, e
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
func (m *Manager) Emit(name string, stackID int64, payload string) error {
	if !validEvent(name) || reservedEvent(name) || len(payload) > 16000 || stackID < 0 {
		return errors.New("自定义事件名无效或内容过长；task/deploy/rebuild/git/project/service/container.* 为内置事件")
	}
	if stackID > 0 {
		if _, err := m.DB.Stack(stackID); err != nil {
			return err
		}
	}
	_, err := m.DB.Exec("INSERT INTO ai_events(name,stack_id,payload,created_at) VALUES(?,?,?,?)", name, stackID, payload, database.Now())
	return err
}
func (m *Manager) schedule(now time.Time) error {
	jobs, err := m.Jobs()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		if err = m.scheduleJob(j, now); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) scheduleJob(j Job, now time.Time) error {
	tx, err := m.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data string
	var enabled bool
	var next, cursor int64
	if err = tx.QueryRow("SELECT config,enabled,next_run,event_cursor FROM ai_jobs WHERE id=?", j.ID).Scan(&data, &enabled, &next, &cursor); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	jobID := j.ID
	if err = json.Unmarshal([]byte(data), &j); err != nil {
		return err
	}
	j.ID = jobID
	var active int
	if err = tx.QueryRow("SELECT count(*) FROM ai_runs WHERE job_id=? AND status IN ('pending','running')", j.ID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return nil
	}
	trigger, event := "scheduled", ""
	if j.Schedule == "event" {
		var eventID int64
		var payload string
		err = tx.QueryRow("SELECT id,payload FROM ai_events WHERE id>? AND name=? AND (?=0 OR stack_id=?) ORDER BY id LIMIT 1", cursor, j.EventName, j.StackID, j.StackID).Scan(&eventID, &payload)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		event = j.EventName + ": " + payload
		trigger = "event"
		_, err = tx.Exec("UPDATE ai_jobs SET event_cursor=? WHERE id=?", eventID, j.ID)
	} else {
		if next == 0 || next > now.Unix() {
			return nil
		}
		_, err = tx.Exec("UPDATE ai_jobs SET next_run=? WHERE id=?", j.next(now), j.ID)
	}
	if err != nil {
		return err
	}
	if _, err = insertRun(tx, j.ID, data, trigger, event); err != nil {
		return err
	}
	return tx.Commit()
}
func (m *Manager) Start(ctx context.Context) error {
	if _, err := m.DB.Exec("UPDATE ai_runs SET status='cancelled',error='服务退出，执行已中断；请检查工作区后重新运行',finished_at=? WHERE status='running'", database.Now()); err != nil {
		return err
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s, err := m.Settings(false)
				if err != nil || !s.Enabled {
					continue
				}
				if err = m.schedule(time.Now()); err != nil {
					m.DB.Audit("ai", "schedule", "system", "failed")
				}
				m.work(ctx)
			}
		}
	}()
	return nil
}
func (m *Manager) Wait() { m.wg.Wait() }
func (m *Manager) Cancel(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	res, err := m.DB.Exec("UPDATE ai_runs SET status='cancelled',error='管理员取消任务',finished_at=? WHERE id=? AND status IN ('pending','running')", database.Now(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("任务不存在或已结束")
	}
	if m.cancelID == id && m.cancel != nil {
		m.cancel()
	}
	return nil
}
