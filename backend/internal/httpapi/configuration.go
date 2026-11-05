package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"zhan/backend/internal/command"
	"zhan/backend/internal/database"
	"zhan/backend/internal/model"
	"zhan/backend/internal/notify"
	"zhan/backend/internal/proxy"
)

const maxConfigurationBytes = 4 * 1024 * 1024

type backupProject struct {
	Name        string   `json:"name"`
	ProjectName string   `json:"project_name"`
	Directory   string   `json:"project_directory"`
	Files       []string `json:"compose_files"`
	Repository  string   `json:"repository"`
	Branch      string   `json:"branch"`
	Polling     bool     `json:"polling_enabled"`
	Interval    int      `json:"poll_interval"`
	AutoDeploy  bool     `json:"auto_deploy"`
	Notify      bool     `json:"notify_on_failure"`
	NoCache     bool     `json:"no_cache"`
	BuildPull   bool     `json:"build_pull"`
	PullImages  bool     `json:"pull_images"`
}

func backupOf(s model.Stack) backupProject {
	return backupProject{s.Name, s.ProjectName, s.Directory, s.Files, s.Repository, s.Branch, s.Polling, s.Interval, s.AutoDeploy, s.Notify, s.NoCache, s.BuildPull, s.PullImages}
}
func (p backupProject) stack() model.Stack {
	return model.Stack{Name: p.Name, ProjectName: p.ProjectName, Directory: p.Directory, Files: append([]string{}, p.Files...), Repository: p.Repository, Branch: p.Branch, Polling: p.Polling, Interval: p.Interval, AutoDeploy: p.AutoDeploy, Notify: p.Notify, NoCache: p.NoCache, BuildPull: p.BuildPull, PullImages: p.PullImages}
}

type backupSystem struct {
	Retention int `json:"log_retention_days"`
}
type configurationBackup struct {
	Format     string          `json:"format"`
	Version    int             `json:"version"`
	ExportedAt string          `json:"exported_at"`
	Projects   []backupProject `json:"projects"`
	System     *backupSystem   `json:"system"`
	SMTP       *model.SMTP     `json:"smtp"`
	Proxy      *proxy.Config   `json:"proxy"`
}
type configurationPreview struct {
	Revision    string   `json:"revision"`
	Created     int      `json:"created_projects"`
	Updated     int      `json:"updated_projects"`
	Projects    []string `json:"projects"`
	ProxyHosts  int      `json:"proxy_hosts"`
	Retention   int      `json:"log_retention_days"`
	SMTPEnabled bool     `json:"smtp_enabled"`
	Warnings    []string `json:"warnings"`
}

func (s *Server) exportConfiguration(c *gin.Context) {
	ctx := c.Request.Context()
	before, err := s.DB.ConfigurationRevision(ctx)
	if err != nil {
		fail(c, 500, "读取配置失败")
		return
	}
	stacks, err := s.DB.Stacks()
	if err != nil {
		fail(c, 500, "读取项目配置失败")
		return
	}
	mail, err := s.Notify.Settings()
	if err != nil {
		fail(c, 500, "读取邮件配置失败")
		return
	}
	mail.Password, mail.PasswordSet = "", false
	gateway := proxy.Defaults()
	if err := s.DB.GetSetting("proxy_config", &gateway); err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(c, 500, "读取网关配置失败")
		return
	}
	gateway.Settings.DuckToken, gateway.Settings.TokenSet, gateway.Revision = "", false, 0
	for i := range gateway.Hosts {
		gateway.Hosts[i].CertExpires, gateway.Hosts[i].CertError = "", ""
	}
	retention := s.Config.LogRetentionDays
	if err := s.DB.GetSetting("log_retention_days", &retention); err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(c, 500, "读取日志保留设置失败")
		return
	}
	backup := configurationBackup{Format: "zhan-config", Version: 1, ExportedAt: database.Now(), Projects: []backupProject{}, System: &backupSystem{retention}, SMTP: &mail, Proxy: &gateway}
	for _, stack := range stacks {
		backup.Projects = append(backup.Projects, backupOf(stack))
	}
	after, err := s.DB.ConfigurationRevision(ctx)
	if err != nil || before != after {
		fail(c, 409, "配置正在变化，请重试导出")
		return
	}
	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil || len(data) > maxConfigurationBytes-1024 || len(stacks) > 1000 {
		fail(c, 400, "配置超出单次导出上限（4 MiB / 1000 个项目）")
		return
	}
	s.DB.Audit(user(c).Username, "configuration_export", "settings", "success")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="zhan-config-%s.json"`, time.Now().Format("2006-01-02")))
	c.Data(200, "application/json; charset=utf-8", data)
}

func decodeConfiguration(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if err == nil {
		var extra any
		if next := decoder.Decode(&extra); next != io.EOF {
			err = next
			if err == nil {
				err = errors.New("文件只能包含一个 JSON 对象")
			}
		}
	}
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			fail(c, 413, "配置文件不能超过 4 MiB")
		} else {
			fail(c, 400, "配置文件格式无效："+err.Error())
		}
		return false
	}
	return true
}

func (s *Server) validateConfiguration(ctx context.Context, b *configurationBackup) ([]model.Stack, configurationPreview, error) {
	preview := configurationPreview{Projects: []string{}, Warnings: []string{"导入后，文件中项目的定时检查与自动部署均暂停，请在项目设置中按需重新开启。", "网关配置与代理规则将替换为文件中的配置，需手动应用；证书不会导入。"}}
	if b.Format != "zhan-config" || b.Version != 1 {
		return nil, preview, errors.New("不是受支持的栈长配置文件（需要 zhan-config，版本 1）")
	}
	if b.System == nil || b.SMTP == nil || b.Proxy == nil || b.Projects == nil {
		return nil, preview, errors.New("配置文件缺少 projects、system、smtp 或 proxy")
	}
	if len(b.Projects) > 1000 || b.System.Retention < 1 || b.System.Retention > 365 {
		return nil, preview, errors.New("最多导入 1000 个项目，日志保留期限须为 1–365 天")
	}
	if b.SMTP.Password != "" || b.Proxy.Settings.DuckToken != "" {
		return nil, preview, errors.New("配置文件不应包含 SMTP 密码或 DuckDNS Token，请移除后在设置中单独填写")
	}
	b.SMTP.PasswordSet = false
	if err := notify.Validate(*b.SMTP); err != nil {
		return nil, preview, fmt.Errorf("邮件配置：%w", err)
	}
	if err := proxy.Normalize(b.Proxy); err != nil {
		return nil, preview, fmt.Errorf("网关配置：%w", err)
	}
	revision, err := s.DB.ConfigurationRevision(ctx)
	if err != nil {
		return nil, preview, err
	}
	existing, err := s.DB.Stacks()
	if err != nil {
		return nil, preview, err
	}
	names := map[string]bool{}
	for _, project := range existing {
		names[project.ProjectName] = true
	}
	seen := map[string]bool{}
	projects := []model.Stack{}
	if len(b.Projects) > 0 {
		// Validate the current Compose files again rather than a cached preview.
		s.Docker.Invalidate()
	}
	for _, p := range b.Projects {
		if seen[p.ProjectName] {
			return nil, preview, fmt.Errorf("项目 %s 在文件中重复", p.ProjectName)
		}
		seen[p.ProjectName] = true
		project := p.stack()
		if err := s.validateStack(ctx, &project); err != nil {
			return nil, preview, fmt.Errorf("项目 %s：%w", p.ProjectName, err)
		}
		projects = append(projects, project)
		preview.Projects = append(preview.Projects, p.ProjectName)
		if names[p.ProjectName] {
			preview.Updated++
		} else {
			preview.Created++
		}
	}
	oldMail, err := s.Notify.Settings()
	if err != nil {
		return nil, preview, err
	}
	if b.SMTP.Host != "" && b.SMTP.Username != "" && (!sameSMTPConnection(oldMail, *b.SMTP) || !oldMail.PasswordSet) {
		preview.Warnings = append(preview.Warnings, "导入后请重新填写 SMTP 密码（如邮件服务器需要认证）。")
	}
	oldProxy := proxy.Defaults()
	if err := s.DB.GetSetting("proxy_config", &oldProxy); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, preview, err
	}
	if b.Proxy.Settings.DuckDomain != "" && (oldProxy.Settings.DuckDomain != b.Proxy.Settings.DuckDomain || oldProxy.Settings.DuckToken == "") {
		preview.Warnings = append(preview.Warnings, "DuckDNS Token 不在文件中；如使用 DuckDNS，请在导入后填写。")
	}
	after, err := s.DB.ConfigurationRevision(ctx)
	if err != nil {
		return nil, preview, err
	}
	if revision != after {
		return nil, preview, database.ErrConfigurationChanged
	}
	preview.Revision, preview.ProxyHosts, preview.Retention, preview.SMTPEnabled = revision, len(b.Proxy.Hosts), b.System.Retention, b.SMTP.Host != ""
	return projects, preview, nil
}

func sameSMTPConnection(a, b model.SMTP) bool {
	return a.Host == b.Host && a.Port == b.Port && a.Username == b.Username && a.TLS == b.TLS
}

func (s *Server) previewConfiguration(c *gin.Context) {
	var backup configurationBackup
	if !decodeConfiguration(c, &backup) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	_, preview, err := s.validateConfiguration(ctx, &backup)
	if err != nil {
		fail(c, 400, command.CleanError(err))
		return
	}
	c.JSON(200, gin.H{"data": preview})
}

func (s *Server) importConfiguration(c *gin.Context) {
	var input struct {
		Backup   configurationBackup `json:"backup"`
		Revision string              `json:"revision"`
	}
	if !decodeConfiguration(c, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	projects, preview, err := s.validateConfiguration(ctx, &input.Backup)
	if err != nil {
		fail(c, 400, command.CleanError(err))
		return
	}
	if preview.Revision != input.Revision || input.Revision == "" {
		fail(c, 409, database.ErrConfigurationChanged.Error())
		return
	}
	oldMail, err := s.Notify.Settings()
	if err != nil {
		fail(c, 500, "读取邮件设置失败")
		return
	}
	err = s.Proxy.ImportConfiguration(*input.Backup.Proxy, func(gateway proxy.Config) error {
		settings := map[string]json.RawMessage{}
		for key, value := range map[string]any{"smtp": input.Backup.SMTP, "proxy_config": gateway, "log_retention_days": input.Backup.System.Retention} {
			data, err := json.Marshal(value)
			if err != nil {
				return err
			}
			settings[key] = data
		}
		return s.DB.RestoreConfiguration(ctx, projects, settings, input.Revision, !sameSMTPConnection(oldMail, *input.Backup.SMTP))
	})
	s.DB.Audit(user(c).Username, "configuration_import", "settings", result(err))
	if err != nil {
		status := 400
		if errors.Is(err, database.ErrConfigurationChanged) || errors.Is(err, database.ErrBusy) {
			status = 409
		}
		fail(c, status, command.CleanError(err))
		return
	}
	c.JSON(200, gin.H{"data": preview})
}
