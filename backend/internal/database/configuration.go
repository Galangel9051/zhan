package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"zhan/backend/internal/model"
)

var ErrConfigurationChanged = errors.New("配置已在校验后发生变化，请重新校验后再导入")

type configurationReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func configurationRevision(ctx context.Context, q configurationReader) (string, error) {
	h := sha256.New()
	queries := []string{
		`SELECT json_array(s.id,s.name,s.project_name,s.project_directory,s.compose_files,s.desired_state,r.url,r.branch,d.polling_enabled,d.poll_interval,d.auto_deploy,d.notify_on_failure,d.no_cache,d.build_pull,d.pull_images) FROM compose_stacks s JOIN git_repositories r ON r.stack_id=s.id JOIN deployment_configs d ON d.stack_id=s.id ORDER BY s.id`,
		`SELECT json_array(key,value) FROM app_settings WHERE key IN ('smtp','proxy_config','log_retention_days') ORDER BY key`,
		`SELECT json_array(name,ciphertext) FROM credentials WHERE name='smtp_password'`,
	}
	for _, query := range queries {
		rows, err := q.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return "", err
			}
			fmt.Fprintln(h, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Store) ConfigurationRevision(ctx context.Context) (string, error) {
	return configurationRevision(ctx, s)
}

// Restore every configuration change together. Projects not present in the
// backup, their task history, and live desired states are preserved.
func (s *Store) RestoreConfiguration(ctx context.Context, projects []model.Stack, settings map[string]json.RawMessage, revision string, clearSMTPPassword bool) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := configurationRevision(ctx, tx)
	if err != nil {
		return err
	}
	if revision == "" || current != revision {
		return ErrConfigurationChanged
	}
	for _, project := range projects {
		err := tx.QueryRowContext(ctx, "SELECT id,desired_state FROM compose_stacks WHERE project_name=?", project.ProjectName).Scan(&project.ID, &project.Desired)
		create := errors.Is(err, sql.ErrNoRows)
		if err != nil && !create {
			return err
		}
		if create {
			project.Desired = "stopped"
		}
		// A restore cannot silently resume automated deployments.
		project.Polling, project.AutoDeploy = false, false
		if err := saveStack(tx, &project, create); err != nil {
			return fmt.Errorf("项目 %s：%w", project.ProjectName, err)
		}
	}
	for key, value := range settings {
		if key != "smtp" && key != "proxy_config" && key != "log_retention_days" {
			return errors.New("不支持的配置项")
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO app_settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(value)); err != nil {
			return err
		}
	}
	if clearSMTPPassword {
		if _, err := tx.ExecContext(ctx, "DELETE FROM credentials WHERE name='smtp_password'"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
