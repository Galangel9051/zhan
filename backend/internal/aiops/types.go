// Package aiops runs bounded, auditable AI operations inside explicitly selected workspaces.
package aiops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/database"
	"zhan/backend/internal/deploy"
	"zhan/backend/internal/docker"
)

type Settings struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key,omitempty"`
	KeySet  bool   `json:"key_set"`
}
type Job struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	Workspace    string `json:"workspace"`
	StackID      int64  `json:"stack_id"`
	Enabled      bool   `json:"enabled"`
	Writable     bool   `json:"writable"`
	AllowActions bool   `json:"allow_actions"`
	Schedule     string `json:"schedule"` // manual, interval, daily, event
	Interval     int    `json:"interval_seconds"`
	DailyAt      string `json:"daily_at"`
	Timezone     string `json:"timezone"`
	EventName    string `json:"event_name"`
	Timeout      int    `json:"timeout_seconds"`
	MaxSteps     int    `json:"max_steps"`
	NextRun      int64  `json:"next_run"`
}
type Run struct {
	ID       int64  `json:"id"`
	JobID    int64  `json:"job_id"`
	Config   Job    `json:"config"`
	Trigger  string `json:"trigger_type"`
	Event    string `json:"event"`
	Status   string `json:"status"`
	Output   string `json:"output"`
	Error    string `json:"error"`
	Created  string `json:"created_at"`
	Started  string `json:"started_at"`
	Finished string `json:"finished_at"`
}
type Manager struct {
	DB       *database.Store
	Auth     *auth.Auth
	Docker   *docker.Client
	Engine   *deploy.Engine
	Client   *http.Client
	mu       sync.Mutex
	cancelID int64
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func New(db *database.Store, a *auth.Auth, d *docker.Client, e *deploy.Engine) *Manager {
	return &Manager{DB: db, Auth: a, Docker: d, Engine: e, Client: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (m *Manager) Settings(secret bool) (Settings, error) {
	var s Settings
	err := m.DB.GetSetting("ai_provider", &s)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{BaseURL: "https://api.openai.com/v1"}, nil
	}
	if err != nil {
		return s, err
	}
	s.KeySet = s.APIKey != ""
	if secret && s.KeySet {
		s.APIKey, err = m.Auth.Decrypt(s.APIKey)
	} else {
		s.APIKey = ""
	}
	return s, err
}
func (m *Manager) SaveSettings(s Settings) error {
	u, err := url.Parse(s.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("请填写 HTTP(S) API 基础地址，不含凭据或查询参数")
	}
	if strings.TrimSpace(s.Model) == "" || len(s.Model) > 200 || len(s.APIKey) > 4096 {
		return errors.New("请填写有效模型和 API Key")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var old Settings
	if err = m.DB.GetSetting("ai_provider", &old); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if s.APIKey == "" {
		s.APIKey = old.APIKey
	} else if s.APIKey, err = m.Auth.Encrypt(s.APIKey); err != nil {
		return err
	}
	if err = m.DB.Setting("ai_provider", s); err != nil {
		return err
	}
	if !s.Enabled && m.cancel != nil {
		m.cancel()
	}
	return nil
}
func (j Job) next(now time.Time) int64 {
	switch j.Schedule {
	case "interval":
		return now.Add(time.Duration(j.Interval) * time.Second).Unix()
	case "daily":
		loc, err := time.LoadLocation(j.Timezone)
		if err != nil {
			return 0
		}
		var h, min int
		_, _ = fmt.Sscanf(j.DailyAt, "%d:%d", &h, &min)
		n := now.In(loc)
		target := time.Date(n.Year(), n.Month(), n.Day(), h, min, 0, 0, loc)
		if !target.After(now) {
			target = time.Date(n.Year(), n.Month(), n.Day()+1, h, min, 0, 0, loc)
		}
		return target.Unix()
	}
	return 0
}
