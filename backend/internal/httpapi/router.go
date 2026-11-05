package httpapi

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"zhan/backend/internal/auth"
	"zhan/backend/internal/command"
	"zhan/backend/internal/config"
	"zhan/backend/internal/database"
	"zhan/backend/internal/deploy"
	"zhan/backend/internal/docker"
	"zhan/backend/internal/model"
	"zhan/backend/internal/notify"
	"zhan/backend/internal/proxy"
	"zhan/backend/internal/sshkey"
	"zhan/backend/internal/webui"
)

type Server struct {
	DB         *database.Store
	Auth       *auth.Auth
	Docker     *docker.Client
	Engine     *deploy.Engine
	Notify     *notify.Notifier
	Proxy      *proxy.Manager
	Git        *sshkey.Manager
	Config     config.Config
	limitMu    sync.Mutex
	attempts   map[string]attempt
	loginSlots chan struct{}
}
type attempt struct {
	At    time.Time
	Count int
}

func (s *Server) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	_ = r.SetTrustedProxies(nil)
	s.attempts = map[string]attempt{}
	s.loginSlots = make(chan struct{}, 2)
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Header("Cache-Control", "no-store")
			limit := int64(64 * 1024)
			if c.Request.URL.Path == "/api/v1/settings/config/import" || c.Request.URL.Path == "/api/v1/settings/config/import/preview" {
				limit = maxConfigurationBytes
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
			if origin := c.GetHeader("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != c.Request.Host || (u.Scheme != "http" && u.Scheme != "https") {
					fail(c, 403, "跨站请求已拒绝")
					c.Abort()
					return
				}
			}
		}
		c.Next()
	})
	api := r.Group("/api/v1")
	api.GET("/health", func(c *gin.Context) {
		if err := s.DB.PingContext(c.Request.Context()); err != nil {
			fail(c, 503, "数据库不可用")
			return
		}
		c.JSON(200, gin.H{"status": "ok", "version": "1.1.0"})
	})
	api.GET("/auth/status", func(c *gin.Context) {
		var count int
		if err := s.DB.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil {
			fail(c, 500, "读取初始化状态失败")
			return
		}
		c.JSON(200, gin.H{"initialized": count > 0})
	})
	api.POST("/auth/login", s.login)
	api.Use(s.session)
	s.proxyRoutes(api)
	api.GET("/auth/me", func(c *gin.Context) { c.JSON(200, gin.H{"data": user(c)}) })
	api.POST("/auth/password", s.changePassword)
	api.POST("/auth/logout", func(c *gin.Context) {
		token, _ := c.Cookie("zhan_session")
		s.Auth.Logout(token)
		s.cookie(c, "", -1)
		c.Status(204)
	})
	api.GET("/filesystem", s.browseFilesystem)
	api.GET("/git/branches", s.inspectGitBranches)
	api.GET("/containers/stats", s.containerStats)
	api.GET("/stacks", s.stacks)
	api.GET("/stacks/discover", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		items, err := s.Docker.Discover(ctx)
		respond(c, items, err)
	})
	api.POST("/stacks/import", s.importStack)
	api.GET("/stacks/:id", s.stack)
	api.PUT("/stacks/:id", s.updateStack)
	api.GET("/stacks/:id/services", func(c *gin.Context) {
		item, ok := s.getStack(c)
		if !ok {
			return
		}
		s.runtime(c, &item)
		c.JSON(200, gin.H{"data": item.Services, "runtime_error": item.RuntimeError})
	})
	for _, action := range []string{"start", "stop", "restart", "rebuild", "deploy"} {
		api.POST("/stacks/:id/"+action, func(c *gin.Context) { s.enqueue(c, action, "") })
	}
	api.POST("/stacks/:id/git/check", func(c *gin.Context) { s.enqueue(c, "git_check", "") })
	api.POST("/stacks/:id/git/pull", func(c *gin.Context) { s.enqueue(c, "git_pull", "") })
	api.GET("/stacks/:id/git/history", s.gitHistory)
	api.GET("/stacks/:id/git/status", func(c *gin.Context) {
		item, ok := s.getStack(c)
		if ok {
			c.JSON(200, gin.H{"data": item})
		}
	})
	api.PUT("/stacks/:id/polling", s.polling)
	for _, action := range []string{"start", "stop", "restart"} {
		api.POST("/stacks/:id/services/:name/"+action, func(c *gin.Context) { s.enqueue(c, action, c.Param("name")) })
		api.POST("/stacks/:id/containers/:container/"+action, func(c *gin.Context) { s.enqueue(c, action, "") })
	}
	api.GET("/stacks/:id/logs", s.containerLogs)
	api.GET("/stacks/:id/containers/:container/logs", s.containerLogs)
	api.GET("/stacks/:id/services/:name/logs", s.containerLogs)
	api.GET("/tasks", func(c *gin.Context) {
		if c.Request.URL.Query().Has("page") {
			s.pagedTasks(c)
			return
		}
		stack, _ := strconv.ParseInt(c.Query("stack_id"), 10, 64)
		before, _ := strconv.ParseInt(c.Query("before"), 10, 64)
		items, err := s.DB.Tasks(stack, before, bounded(c.Query("limit"), 50, 1, 200))
		respond(c, items, err)
	})
	api.GET("/tasks/:id", func(c *gin.Context) {
		item, ok := s.getTask(c)
		if ok {
			c.JSON(200, gin.H{"data": item})
		}
	})
	api.POST("/tasks/:id/cancel", func(c *gin.Context) {
		t, ok := s.getTask(c)
		if !ok {
			return
		}
		err := s.Engine.Cancel(t.ID)
		s.DB.Audit(user(c).Username, "task_cancel", fmt.Sprint(t.ID), result(err))
		if err != nil {
			fail(c, 409, command.CleanError(err))
			return
		}
		c.Status(204)
	})
	api.POST("/tasks/:id/retry", func(c *gin.Context) {
		t, ok := s.getTask(c)
		if !ok {
			return
		}
		if t.Status != "failed" && t.Status != "cancelled" {
			fail(c, 409, "仅失败或取消的任务可重试")
			return
		}
		t.ID = 0
		t.Trigger = "manual"
		s.createTask(c, &t)
	})
	api.GET("/tasks/:id/logs", s.logs)
	api.GET("/tasks/:id/stream", s.stream)
	api.GET("/settings", s.settings)
	api.GET("/settings/git", s.gitSettings)
	api.POST("/settings/git/regenerate", s.regenerateGitKey)
	api.POST("/settings/git/test", s.testGitKey)
	api.POST("/settings/git/known-hosts/reset", s.resetGitKnownHosts)
	api.GET("/settings/config/export", s.exportConfiguration)
	api.POST("/settings/config/import/preview", s.previewConfiguration)
	api.POST("/settings/config/import", s.importConfiguration)
	api.PUT("/settings", s.updateSettings)
	api.PUT("/settings/smtp", func(c *gin.Context) {
		var input model.SMTP
		if !bind(c, &input) {
			return
		}
		err := s.Notify.Save(input)
		s.DB.Audit(user(c).Username, "smtp_update", "system", result(err))
		if err != nil {
			fail(c, 400, command.CleanError(err))
			return
		}
		cfg, err := s.Notify.Settings()
		respond(c, cfg, err)
	})
	api.POST("/settings/smtp/test", func(c *gin.Context) {
		var input struct {
			Config *model.SMTP `json:"config"`
		}
		if c.Request.ContentLength != 0 {
			if !bind(c, &input) {
				return
			}
		}
		var err error
		if input.Config != nil {
			err = s.Notify.TestConfig(*input.Config)
		} else {
			err = s.Notify.Test()
		}
		s.DB.Audit(user(c).Username, "smtp_test", "system", result(err))
		if err != nil {
			fail(c, 400, "测试邮件发送失败："+command.CleanError(err))
			return
		}
		c.JSON(200, gin.H{"message": "测试邮件已发送"})
	})
	api.GET("/audit", func(c *gin.Context) { items, err := s.DB.Audits(); respond(c, items, err) })
	api.GET("/notifications", s.notifications)
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			fail(c, 404, "接口不存在")
			return
		}
		// Gin 进入 NoRoute 前已把状态码预设为 404；这里交回前端决定最终状态码。
		c.Status(http.StatusOK)
		webui.Handler().ServeHTTP(c.Writer, c.Request)
	})
	return r
}
func (s *Server) session(c *gin.Context) {
	token, _ := c.Cookie("zhan_session")
	u, err := s.Auth.Session(token)
	if err != nil {
		fail(c, 401, "请先登录")
		c.Abort()
		return
	}
	if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
		csrf := c.GetHeader("X-CSRF-Token")
		if subtle.ConstantTimeCompare([]byte(csrf), []byte(u.CSRF)) != 1 {
			fail(c, 403, "CSRF 验证失败，请刷新后重试")
			c.Abort()
			return
		}
	}
	if u.MustChangePassword && c.Request.URL.Path != "/api/v1/auth/me" && c.Request.URL.Path != "/api/v1/auth/password" && c.Request.URL.Path != "/api/v1/auth/logout" {
		c.JSON(403, gin.H{"error": "首次登录必须修改密码后才能使用管理功能", "code": "password_change_required"})
		c.Abort()
		return
	}
	c.Set("user", u)
	c.Next()
}
func (s *Server) cookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{Name: "zhan_session", Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: s.Config.SecureCookies, SameSite: http.SameSiteStrictMode})
}
func (s *Server) allowLogin(ip string) bool {
	s.limitMu.Lock()
	defer s.limitMu.Unlock()
	now := time.Now()
	for key, value := range s.attempts {
		if now.Sub(value.At) > time.Minute {
			delete(s.attempts, key)
		}
	}
	if len(s.attempts) > 1024 {
		return false
	}
	for _, key := range []string{"global", ip} {
		value := s.attempts[key]
		if value.At.IsZero() {
			value.At = now
		}
		maxCount := 5
		if key == "global" {
			maxCount = 30
		}
		if value.Count >= maxCount {
			return false
		}
	}
	for _, key := range []string{"global", ip} {
		value := s.attempts[key]
		if value.At.IsZero() {
			value.At = now
		}
		value.Count++
		s.attempts[key] = value
	}
	return true
}
func (s *Server) login(c *gin.Context) {
	if !s.allowLogin(c.ClientIP()) {
		c.Header("Retry-After", "60")
		fail(c, 429, "登录尝试过于频繁，请一分钟后重试")
		return
	}
	select {
	case s.loginSlots <- struct{}{}:
		defer func() { <-s.loginSlots }()
	default:
		fail(c, 429, "登录服务繁忙，请稍后重试")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bind(c, &input) {
		return
	}
	if len(input.Username) > 64 || len(input.Password) > 128 {
		fail(c, 400, "用户名或密码长度无效")
		return
	}
	token, u, err := s.Auth.Login(input.Username, input.Password)
	s.DB.Audit(input.Username, "login", "system", result(err))
	if err != nil {
		fail(c, 401, "用户名或密码错误")
		return
	}
	s.cookie(c, token, s.Config.SessionHours*3600)
	c.JSON(200, gin.H{"data": u})
}
func user(c *gin.Context) auth.User { return c.MustGet("user").(auth.User) }
func (s *Server) runtime(c *gin.Context, item *model.Stack) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := s.Docker.Status(ctx, item); err != nil {
		item.Runtime = "unknown"
		item.RuntimeError = command.CleanError(err)
	}
	if tasks, err := s.DB.Tasks(item.ID, 0, 1); err == nil && len(tasks) > 0 {
		item.LastTask = &tasks[0]
	}
}
func (s *Server) stacks(c *gin.Context) {
	items, err := s.DB.Stacks()
	if err != nil {
		fail(c, 500, "读取项目失败")
		return
	}
	for i := range items {
		if c.Request.Context().Err() != nil {
			return
		}
		s.runtime(c, &items[i])
	}
	c.JSON(200, gin.H{"data": items})
}
func (s *Server) stack(c *gin.Context) {
	item, ok := s.getStack(c)
	if !ok {
		return
	}
	s.runtime(c, &item)
	c.JSON(200, gin.H{"data": item})
}
func (s *Server) getStack(c *gin.Context) (model.Stack, bool) {
	id, ok := idParam(c)
	if !ok {
		return model.Stack{}, false
	}
	item, err := s.DB.Stack(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(c, 404, "项目不存在")
		} else {
			fail(c, 500, "读取项目失败")
		}
		return item, false
	}
	return item, true
}
func (s *Server) getTask(c *gin.Context) (model.Task, bool) {
	id, ok := idParam(c)
	if !ok {
		return model.Task{}, false
	}
	item, err := s.DB.Task(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(c, 404, "任务不存在")
		} else {
			fail(c, 500, "读取任务失败")
		}
		return item, false
	}
	return item, true
}
func (s *Server) validateStack(ctx context.Context, item *model.Stack) error {
	if item.Interval < 30 || item.Interval > 86400 {
		return errors.New("轮询间隔须为 30–86400 秒")
	}
	if item.AutoDeploy && !item.Polling {
		return errors.New("自动部署需要启用轮询")
	}
	if err := s.Docker.Validate(item); err != nil {
		return fmt.Errorf("项目路径校验失败：%w", err)
	}
	if _, err := s.Docker.Config(ctx, *item); err != nil {
		return fmt.Errorf("Compose 配置校验失败：%w", err)
	}
	if err := s.Engine.ValidateGit(ctx, item); err != nil {
		return fmt.Errorf("Git 绑定校验失败：%w。如果只需管理容器，可清空 Git 分支后重新导入", deploy.ExplainGitError(err))
	}
	return nil
}
func (s *Server) importStack(c *gin.Context) {
	item := model.Stack{Interval: 300, Notify: true, PullImages: true}
	if !bind(c, &item) {
		return
	}
	item.ID = 0
	item.Desired = "stopped"
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if err := s.validateStack(ctx, &item); err != nil {
		fail(c, 400, command.CleanError(err))
		return
	}
	// Import only records bindings; it never changes Docker or the working tree.
	if err := s.Docker.Status(ctx, &item); err != nil {
		fail(c, 400, command.CleanError(err))
		return
	}
	if item.Runtime == "running" || item.Runtime == "partial" || item.Runtime == "unhealthy" {
		item.Desired = "running"
	}
	if err := s.DB.SaveStack(&item, true); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			fail(c, 409, fmt.Sprintf("Compose 项目 %q 已被导入，请在项目列表中查看或修改现有配置", item.ProjectName))
			return
		}
		log.Printf("stack import persistence: %s", command.CleanError(err))
		fail(c, 500, "导入配置保存失败，请查看服务日志，检查数据库目录的权限和可用空间")
		return
	}
	s.DB.Audit(user(c).Username, "stack_import", item.ProjectName, "success")
	c.JSON(201, gin.H{"data": item})
}
func (s *Server) updateStack(c *gin.Context) {
	old, ok := s.getStack(c)
	if !ok {
		return
	}
	item := old
	if !bind(c, &item) {
		return
	}
	item.ID = old.ID
	item.Desired = old.Desired
	// A different project identity requires a separate explicit import.
	if item.ProjectName != old.ProjectName || item.Directory != old.Directory {
		fail(c, 400, "修改项目身份或根目录请使用重新导入，避免指向另一套容器")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if err := s.validateStack(ctx, &item); err != nil {
		fail(c, 400, command.CleanError(err))
		return
	}
	if err := s.DB.SaveStack(&item, false); err != nil {
		fail(c, 409, command.CleanError(err))
		return
	}
	s.Docker.Invalidate()
	s.DB.Audit(user(c).Username, "stack_update", item.ProjectName, "success")
	respond(c, item, nil)
}
func (s *Server) polling(c *gin.Context) {
	item, ok := s.getStack(c)
	if !ok {
		return
	}
	var input struct {
		Enabled  bool `json:"polling_enabled"`
		Interval int  `json:"poll_interval"`
		Auto     bool `json:"auto_deploy"`
	}
	if !bind(c, &input) {
		return
	}
	item.Polling = input.Enabled
	item.Interval = input.Interval
	item.AutoDeploy = input.Auto
	if item.Interval < 30 || item.Interval > 86400 || item.Branch == "" || (item.AutoDeploy && !item.Polling) {
		fail(c, 400, "请先绑定分支，并配置 30–86400 秒轮询间隔")
		return
	}
	if err := s.DB.SaveStack(&item, false); err != nil {
		fail(c, 409, command.CleanError(err))
		return
	}
	s.DB.Audit(user(c).Username, "polling_update", item.ProjectName, "success")
	respond(c, item, nil)
}
func (s *Server) enqueue(c *gin.Context, kind, service string) {
	item, ok := s.getStack(c)
	if !ok {
		return
	}
	var input struct {
		StartAfter bool `json:"start_after_deploy"`
	}
	if c.Request.ContentLength != 0 {
		if !bind(c, &input) {
			return
		}
	}
	t := model.Task{StackID: item.ID, Type: kind, Service: service, Container: c.Param("container"), StartAfter: input.StartAfter}
	s.createTask(c, &t)
}
func (s *Server) createTask(c *gin.Context, t *model.Task) {
	err := s.Engine.Enqueue(t)
	s.DB.Audit(user(c).Username, "task_"+t.Type, fmt.Sprint(t.StackID), result(err))
	if err != nil {
		fail(c, 409, command.CleanError(err))
		return
	}
	saved, err := s.DB.Task(t.ID)
	if err != nil {
		fail(c, 500, "任务已排队，但读取失败")
		return
	}
	c.JSON(202, gin.H{"data": saved})
}
func (s *Server) logs(c *gin.Context) {
	t, ok := s.getTask(c)
	if !ok {
		return
	}
	path := s.Engine.LogPath(t.ID)
	if c.Query("download") == "1" {
		if _, err := os.Stat(path); err != nil {
			fail(c, 404, "日志已过期清理或尚未生成")
			return
		}
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.FileAttachment(path, fmt.Sprintf("task-%d.log", t.ID))
		return
	}
	offset := int64(-1)
	if c.Query("offset") != "" {
		offset, _ = strconv.ParseInt(c.Query("offset"), 10, 64)
		if offset < 0 {
			offset = 0
		}
	}
	text, next, err := deploy.ReadLog(path, offset, bounded(c.Query("limit"), 65536, 1024, 262144))
	if err != nil {
		fail(c, 500, "读取日志失败")
		return
	}
	c.JSON(200, gin.H{"data": text, "next_offset": next, "status": t.Status})
}
func (s *Server) stream(c *gin.Context) {
	task, ok := s.getTask(c)
	if !ok {
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	offset := int64(-1)
	if q := c.Query("offset"); q != "" {
		offset, _ = strconv.ParseInt(q, 10, 64)
		if offset < 0 {
			offset = 0
		}
	}
	if last := c.GetHeader("Last-Event-ID"); last != "" {
		if n, err := strconv.ParseInt(last, 10, 64); err == nil && n >= 0 {
			offset = n
		}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := 0
	for {
		if _, err := s.Auth.Session(cookieValue(c)); err != nil {
			return
		}
		text, next, err := deploy.ReadLog(s.Engine.LogPath(task.ID), offset, 65536)
		if err != nil {
			c.SSEvent("error", gin.H{"message": "读取日志失败"})
			c.Writer.Flush()
			return
		}
		if text != "" {
			fmt.Fprintf(c.Writer, "id: %d\n", next)
			c.SSEvent("log", gin.H{"text": text, "offset": next})
			c.Writer.Flush()
		}
		offset = next
		fresh, err := s.DB.Task(task.ID)
		if err != nil {
			return
		}
		if fresh.Status != "pending" && fresh.Status != "running" && text == "" {
			c.SSEvent("done", gin.H{"status": fresh.Status})
			c.Writer.Flush()
			return
		}
		heartbeat++
		if heartbeat%10 == 0 {
			fmt.Fprint(c.Writer, ": heartbeat\n\n")
			c.Writer.Flush()
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
func cookieValue(c *gin.Context) string { v, _ := c.Cookie("zhan_session"); return v }
func (s *Server) containerLogs(c *gin.Context) {
	stack, ok := s.getStack(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	service := c.Param("name")
	if _, err := s.Docker.CheckTarget(ctx, stack, service, c.Param("container")); err != nil {
		fail(c, 400, command.CleanError(err))
		return
	}
	args := []string{"logs", "--no-color", "--timestamps", "--tail", strconv.Itoa(bounded(c.Query("tail"), 200, 1, 2000))}
	if service != "" {
		args = append(args, service)
	}
	commandArgs := docker.Args(stack, args...)
	if container := c.Param("container"); container != "" {
		commandArgs = []string{"logs", "--timestamps", "--tail", strconv.Itoa(bounded(c.Query("tail"), 200, 1, 2000)), container}
	}
	data, err := command.Capture(ctx, s.Docker.Runner, stack.Directory, "docker", commandArgs...)
	if err != nil {
		fail(c, 400, command.CleanError(err))
		return
	}
	c.JSON(200, gin.H{"data": command.Redact(data)})
}
func (s *Server) settings(c *gin.Context) {
	smtp, err := s.Notify.Settings()
	if err != nil {
		fail(c, 500, "读取邮件设置失败")
		return
	}
	retention := s.Config.LogRetentionDays
	_ = s.DB.GetSetting("log_retention_days", &retention)
	c.JSON(200, gin.H{"data": gin.H{"smtp": smtp, "listen": s.Config.Listen, "allowed_roots": s.Config.AllowedRoots, "workers": s.Config.Workers, "log_retention_days": retention, "max_log_mb": s.Config.MaxLogMB, "retry_delays_seconds": s.Config.RetryDelays, "secure_cookies": s.Config.SecureCookies}})
}
func (s *Server) updateSettings(c *gin.Context) {
	var input struct {
		Retention int `json:"log_retention_days"`
	}
	if !bind(c, &input) {
		return
	}
	if input.Retention < 1 || input.Retention > 365 {
		fail(c, 400, "日志保留期限须为 1–365 天")
		return
	}
	if err := s.DB.Setting("log_retention_days", input.Retention); err != nil {
		fail(c, 500, "保存设置失败")
		return
	}
	s.DB.Audit(user(c).Username, "settings_update", "system", "success")
	c.Status(204)
}
func (s *Server) notifications(c *gin.Context) {
	rows, err := s.DB.Query("SELECT id,coalesce(task_id,0),recipient,notification_type,status,sent_at,error_message FROM notification_logs ORDER BY id DESC LIMIT 100")
	if err != nil {
		fail(c, 500, "读取通知记录失败")
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, task, at int64
		var recipient, kind, status, msg string
		if err = rows.Scan(&id, &task, &recipient, &kind, &status, &at, &msg); err != nil {
			fail(c, 500, "读取通知记录失败")
			return
		}
		items = append(items, gin.H{"id": id, "task_id": task, "recipient": recipient, "notification_type": kind, "status": status, "sent_at": at, "error_message": msg})
	}
	c.JSON(200, gin.H{"data": items})
}
func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		fail(c, 400, "无效 ID")
		return 0, false
	}
	return id, true
}
func bind(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		fail(c, 400, "请求 JSON 格式或字段类型错误")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(c, 400, "请求须为单个 JSON 对象")
		return false
	}
	return true
}
func fail(c *gin.Context, status int, message string) { c.JSON(status, gin.H{"error": message}) }
func respond(c *gin.Context, data any, err error) {
	if err != nil {
		fail(c, 500, command.CleanError(err))
		return
	}
	c.JSON(200, gin.H{"data": data})
}
func result(err error) string {
	if err == nil {
		return "success"
	}
	return "failed: " + command.CleanError(err)
}
func bounded(value string, fallback, low, high int) int {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return max(low, min(high, n))
}

func (s *Server) changePassword(c *gin.Context) {
	var input struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
		Confirm string `json:"confirm_password"`
	}
	if !bind(c, &input) {
		return
	}
	if len(input.Current) > 128 || input.Next != input.Confirm {
		fail(c, 400, "两次输入的新密码不一致或当前密码无效")
		return
	}
	// Password verification is CPU-intensive; share the bounded login worker slots.
	select {
	case s.loginSlots <- struct{}{}:
		defer func() { <-s.loginSlots }()
	default:
		fail(c, 429, "认证服务繁忙，请稍后重试")
		return
	}
	if !s.allowLogin("password:" + fmt.Sprint(user(c).ID)) {
		fail(c, 429, "密码修改尝试过于频繁，请一分钟后重试")
		return
	}
	err := s.Auth.ChangePassword(user(c).ID, input.Current, input.Next)
	s.DB.Audit(user(c).Username, "password_change", "self", result(err))
	if err != nil {
		fail(c, 400, command.CleanError(err))
		return
	}
	s.cookie(c, "", -1)
	c.Status(204)
}
