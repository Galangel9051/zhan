-- Events are emitted only once per transition. AI-created operations never recurse.
DROP TRIGGER ai_task_event;
CREATE TRIGGER ai_task_queued AFTER INSERT ON tasks WHEN NEW.ai_run_id=0
BEGIN
 INSERT INTO ai_events(name,stack_id,payload,created_at) VALUES('task.queued',NEW.stack_id,json_object('task_id',NEW.id,'task_type',NEW.task_type,'status',NEW.status,'error',NEW.error_message,'service',NEW.service,'container_id',NEW.container_id,'commit_sha',NEW.commit_sha,'trigger_type',NEW.trigger_type),strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;
CREATE TRIGGER ai_task_event AFTER UPDATE OF status ON tasks
WHEN NEW.status IN ('running','success','failed','cancelled') AND OLD.status != NEW.status AND NEW.ai_run_id=0
BEGIN
 INSERT INTO ai_events(name,stack_id,payload,created_at) VALUES('task.' || CASE NEW.status WHEN 'running' THEN 'started' ELSE NEW.status END,NEW.stack_id,json_object('task_id',NEW.id,'task_type',NEW.task_type,'status',NEW.status,'error',NEW.error_message,'service',NEW.service,'container_id',NEW.container_id,'commit_sha',NEW.commit_sha,'trigger_type',NEW.trigger_type),strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;
CREATE TRIGGER ai_task_specific_event AFTER UPDATE OF status ON tasks
WHEN NEW.status IN ('success','failed') AND OLD.status != NEW.status AND NEW.ai_run_id=0
 AND NEW.task_type IN ('deploy','rebuild','git_check','git_pull','start','stop','restart')
BEGIN
 INSERT INTO ai_events(name,stack_id,payload,created_at) VALUES(CASE NEW.task_type WHEN 'git_check' THEN 'git.check.'||NEW.status WHEN 'git_pull' THEN 'git.pull.'||NEW.status WHEN 'deploy' THEN 'deploy.'||NEW.status WHEN 'rebuild' THEN 'rebuild.'||NEW.status ELSE (CASE WHEN NEW.container_id!='' THEN 'container.' WHEN NEW.service!='' THEN 'service.' ELSE 'project.' END) || (CASE WHEN NEW.status='failed' THEN 'action_failed' WHEN NEW.task_type='start' THEN 'started' WHEN NEW.task_type='stop' THEN 'stopped' ELSE 'restarted' END) END,NEW.stack_id,json_object('task_id',NEW.id,'task_type',NEW.task_type,'status',NEW.status,'error',NEW.error_message,'service',NEW.service,'container_id',NEW.container_id,'commit_sha',NEW.commit_sha,'trigger_type',NEW.trigger_type),strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;
CREATE TRIGGER ai_git_updated AFTER UPDATE OF remote_commit ON deployment_configs
WHEN NEW.remote_commit!='' AND NEW.remote_commit!=OLD.remote_commit AND NEW.remote_commit!=NEW.local_commit
 AND NOT EXISTS(SELECT 1 FROM tasks WHERE stack_id=NEW.stack_id AND status='running' AND ai_run_id>0)
BEGIN
 INSERT INTO ai_events(name,stack_id,payload,created_at) VALUES('git.updated',NEW.stack_id,json_object('remote_commit',NEW.remote_commit,'local_commit',NEW.local_commit,'previous_remote_commit',OLD.remote_commit),strftime('%Y-%m-%dT%H:%M:%fZ','now'));
END;
