package notify

import (
	"zhan/backend/internal/auth"
	"zhan/backend/internal/command"
	"zhan/backend/internal/database"
	"zhan/backend/internal/model"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type Notifier struct {
	DB   *database.Store
	Auth *auth.Auth
}

func (n *Notifier) Settings() (model.SMTP, error) {
	s := model.SMTP{Port: 587, TLS: "starttls", Recipients: []string{}, Stages: []string{"git_pull", "build", "pull_images", "deploy", "healthcheck"}, Cooldown: 30}
	err := n.DB.GetSetting("smtp", &s)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return s, err
	}
	var count int
	err = n.DB.QueryRow("SELECT count(*) FROM credentials WHERE name='smtp_password'").Scan(&count)
	s.Password = ""
	s.PasswordSet = count > 0
	return s, err
}
func validateSMTP(s model.SMTP) error {
	if s.Host != "" {
		if strings.ContainsAny(s.Host, "\r\n /\\") || s.Port < 1 || s.Port > 65535 {
			return errors.New("SMTP 地址或端口无效")
		}
		if s.TLS != "starttls" && s.TLS != "tls" && s.TLS != "none" {
			return errors.New("无效 TLS 模式")
		}
		if err := addresses(s); err != nil {
			return err
		}
	}
	if s.Cooldown < 1 || s.Cooldown > 1440 {
		return errors.New("邮件去重时间须为 1–1440 分钟")
	}
	valid := map[string]bool{"git_check": true, "git_pull": true, "build": true, "pull_images": true, "deploy": true, "healthcheck": true}
	for _, stage := range s.Stages {
		if !valid[stage] {
			return errors.New("无效通知阶段")
		}
	}
	if len(s.Password) > 1024 || strings.ContainsAny(s.Username, "\r\n") {
		return errors.New("SMTP 凭据无效")
	}
	return nil
}
func (n *Notifier) Save(s model.SMTP) error {
	if err := validateSMTP(s); err != nil {
		return err
	}
	if s.Password != "" {
		encrypted, err := n.Auth.Encrypt(s.Password)
		if err != nil {
			return err
		}
		if _, err = n.DB.Exec("INSERT INTO credentials(name,ciphertext) VALUES('smtp_password',?) ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext", encrypted); err != nil {
			return err
		}
	}
	s.Password = ""
	s.PasswordSet = false
	return n.DB.Setting("smtp", s)
}
func addresses(s model.SMTP) error {
	if len(s.Recipients) < 1 || len(s.Recipients) > 20 {
		return errors.New("请填写 1–20 个收件人")
	}
	for _, v := range append([]string{s.From}, s.Recipients...) {
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("邮件地址无效")
		}
		addr, err := mail.ParseAddress(v)
		if err != nil || addr.Address != v {
			return errors.New("请填写纯邮箱地址，不包含显示名称")
		}
	}
	return nil
}
func (n *Notifier) Send(s model.SMTP, subject, body string) error {
	if s.Host == "" {
		return errors.New("尚未配置 SMTP")
	}
	if err := validateSMTP(s); err != nil {
		return err
	}
	password := s.Password
	var err error
	if password == "" && s.Username != "" {
		saved, loadErr := n.Settings()
		if loadErr != nil {
			return loadErr
		}
		if saved.Host != s.Host || saved.Port != s.Port || saved.Username != s.Username || saved.TLS != s.TLS {
			return errors.New("SMTP 连接信息已更改，请重新填写密码后再测试")
		}
		var encrypted string
		err = n.DB.QueryRow("SELECT ciphertext FROM credentials WHERE name='smtp_password'").Scan(&encrypted)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("请填写 SMTP 密码")
		}
		if err != nil {
			return err
		}
		password, err = n.Auth.Decrypt(encrypted)
		if err != nil {
			return err
		}
	}
	address := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	if s.TLS == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if s.TLS == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP 服务器不支持 STARTTLS")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", s.Username, password, s.Host)); err != nil {
			return err
		}
	}
	if err = client.Mail(s.From); err != nil {
		return err
	}
	for _, recipient := range s.Recipients {
		if err = client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	subject = strings.NewReplacer("\r", "", "\n", "").Replace(subject)
	_, err = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", s.From, strings.Join(s.Recipients, ", "), subject, strings.ReplaceAll(body, "\n", "\r\n"))
	if err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
func (n *Notifier) Failure(t model.Task, s model.Stack) {
	cfg, err := n.Settings()
	if err != nil || !s.Notify || cfg.Host == "" {
		return
	}
	enabled := false
	for _, v := range cfg.Stages {
		if v == t.Stage {
			enabled = true
		}
	}
	if !enabled {
		return
	}
	var count int
	_ = n.DB.QueryRow("SELECT count(*) FROM notification_logs WHERE stack_id=? AND commit_sha=? AND stage=? AND sent_at>?", s.ID, t.Commit, t.Stage, time.Now().Unix()-int64(cfg.Cooldown*60)).Scan(&count)
	if count > 0 {
		return
	}
	body := fmt.Sprintf("项目：%s\n分支：%s\nCommit：%s\n执行时间（UTC）：%s\n失败阶段：%s\n退出码：%d\n\n错误摘要：\n%s\n\n请登录栈长查看任务 #%d 完整日志。", s.Name, s.Branch, t.Commit, database.Now(), t.Stage, t.ExitCode, t.Error, t.ID)
	err = n.Send(cfg, "[栈长] "+s.Name+" 执行失败", body)
	status := "success"
	if err != nil {
		status = "failed"
	}
	for _, recipient := range cfg.Recipients {
		_, _ = n.DB.Exec("INSERT INTO notification_logs(task_id,stack_id,recipient,notification_type,commit_sha,stage,status,sent_at,error_message) VALUES(?,?,?,?,?,?,?,?,?)", t.ID, s.ID, recipient, "failure", t.Commit, t.Stage, status, time.Now().Unix(), command.CleanError(err))
	}
}
func (n *Notifier) Test() error {
	cfg, err := n.Settings()
	if err != nil {
		return err
	}
	return n.TestConfig(cfg)
}

// TestConfig sends with the submitted settings without saving them.
func (n *Notifier) TestConfig(cfg model.SMTP) error {
	if cfg.Cooldown == 0 {
		cfg.Cooldown = 30
	}
	err := n.Send(cfg, "[栈长] 测试邮件", "SMTP 配置验证成功。\n此邮件由管理员手动触发，使用当前填写的配置。")
	if err != nil && cfg.Password != "" {
		err = errors.New(strings.ReplaceAll(err.Error(), cfg.Password, "[REDACTED]"))
	}
	status := "success"
	if err != nil {
		status = "failed"
	}
	for _, recipient := range cfg.Recipients {
		_, _ = n.DB.Exec("INSERT INTO notification_logs(recipient,notification_type,status,sent_at,error_message) VALUES(?,'test',?,?,?)", recipient, status, time.Now().Unix(), command.CleanError(err))
	}
	return err
}
