CREATE TABLE ai_jobs(id INTEGER PRIMARY KEY, config TEXT NOT NULL, enabled INTEGER NOT NULL, next_run INTEGER NOT NULL DEFAULT 0, event_cursor INTEGER NOT NULL DEFAULT 0);
CREATE TABLE ai_runs(id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL REFERENCES ai_jobs(id), config TEXT NOT NULL, trigger_type TEXT NOT NULL, event TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'pending', output TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, started_at TEXT NOT NULL DEFAULT '', finished_at TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX ai_one_active_job ON ai_runs(job_id) WHERE status IN ('pending','running');
CREATE TABLE ai_events(id INTEGER PRIMARY KEY, name TEXT NOT NULL, stack_id INTEGER NOT NULL DEFAULT 0, payload TEXT NOT NULL, created_at TEXT NOT NULL);
ALTER TABLE tasks ADD COLUMN ai_run_id INTEGER NOT NULL DEFAULT 0;
CREATE TRIGGER ai_task_event AFTER UPDATE OF status ON tasks
WHEN NEW.status IN ('success','failed') AND OLD.status != NEW.status AND NEW.ai_run_id=0
BEGIN
 INSERT INTO ai_events(name,stack_id,payload,created_at) VALUES('task.'||NEW.status,NEW.stack_id,json_object('task_id',NEW.id,'task_type',NEW.task_type,'status',NEW.status,'error',NEW.error_message),NEW.finished_at);
END;
