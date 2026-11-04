package proxy

import (
	"compose-manager/backend/internal/auth"
	"compose-manager/backend/internal/command"
	"compose-manager/backend/internal/database"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Job struct {
	ID       int64  `json:"id"`
	Action   string `json:"action"`
	HostID   string `json:"host_id"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Created  string `json:"created_at"`
	Finished string `json:"finished_at"`
}
type State struct {
	Config
	Jobs         []Job  `json:"jobs"`
	Busy         bool   `json:"busy"`
	Dirty        bool   `json:"dirty"`
	Runtime      string `json:"runtime"`
	RuntimeError string `json:"runtime_error,omitempty"`
	Preview      string `json:"preview"`
}
type Manager struct {
	httpClient    *http.Client
	acmeDirectory string
	checkTXT      func(context.Context, string, string) error

	DB     *database.Store
	Auth   *auth.Auth
	Runner command.Runner
	Dir    string
	mu     sync.Mutex
	busy   bool
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(db *database.Store, a *auth.Auth, r command.Runner, dataDir string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{DB: db, Auth: a, Runner: r, Dir: filepath.Join(dataDir, "proxy"), ctx: ctx, cancel: cancel}
}
func (m *Manager) Start() error {
	if err := os.MkdirAll(filepath.Join(m.Dir, "certs"), 0700); err != nil {
		return err
	}
	if _, err := m.DB.Exec("UPDATE proxy_jobs SET status='failed',message='服务重启，任务中断；请检查 Nginx 状态后重试',finished_at=? WHERE status='running'", database.Now()); err != nil {
		return err
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		tick := time.NewTicker(5 * time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-tick.C:
				m.maintain()
			}
		}
	}()
	return nil
}
func (m *Manager) Close() { m.cancel(); m.wg.Wait() }
func (m *Manager) load() (Config, error) {
	c := Defaults()
	err := m.DB.GetSetting("proxy_config", &c)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return c, err
}
func (m *Manager) State(ctx context.Context) (State, error) {
	m.mu.Lock()
	c, err := m.load()
	busy := m.busy
	m.mu.Unlock()
	if err != nil {
		return State{}, err
	}
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if h.HTTPS {
			cert, e := m.certificate(*h, c.Settings)
			if e != nil {
				h.CertError = "尚无有效证书，请先申请"
			} else {
				h.CertExpires = cert.NotAfter.UTC().Format(time.RFC3339)
			}
		}
	}
	c.Settings.TokenSet = c.Settings.DuckToken != ""
	c.Settings.DuckToken = ""
	jobs := []Job{}
	rows, err := m.DB.Query("SELECT id,action,host_id,status,message,created_at,finished_at FROM proxy_jobs ORDER BY id DESC LIMIT 30")
	if err != nil {
		return State{}, err
	}
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.ID, &j.Action, &j.HostID, &j.Status, &j.Message, &j.Created, &j.Finished); err != nil {
			break
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err != nil {
		return State{}, err
	}
	active := Defaults()
	_ = m.DB.GetSetting("proxy_active", &active)
	state := State{Config: c, Jobs: jobs, Busy: busy, Dirty: fingerprint(c) != fingerprint(active), Runtime: "stopped", Preview: Render(c)}
	if _, err = os.Stat(filepath.Join(m.Dir, "compose.yaml")); err == nil {
		out, e := m.compose(ctx, filepath.Join(m.Dir, "compose.yaml"), "ps", "--format", "json")
		if e != nil {
			state.Runtime = "unknown"
			state.RuntimeError = e.Error()
		} else if strings.Contains(out, `"State":"running"`) || strings.Contains(out, `"State": "running"`) {
			state.Runtime = "running"
		}
	}
	return state, nil
}
func (m *Manager) Save(c Config) (Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy {
		return c, errors.New("Nginx 正在执行任务，请稍后保存")
	}
	old, err := m.load()
	if err != nil {
		return c, err
	}
	if old.Revision != c.Revision {
		return c, errors.New("配置已被其他窗口修改，请刷新后重试")
	}
	if err = Normalize(&c); err != nil {
		return c, err
	}
	if c.Settings.DuckToken == "" {
		c.Settings.DuckToken = old.Settings.DuckToken
	} else {
		if len(c.Settings.DuckToken) > 512 || strings.ContainsAny(c.Settings.DuckToken, "\r\n") {
			return c, errors.New("DuckDNS Token 格式无效")
		}
		c.Settings.DuckToken, err = m.Auth.Encrypt(c.Settings.DuckToken)
		if err != nil {
			return c, err
		}
	}
	c.Settings.TokenSet = c.Settings.DuckToken != ""
	c.Revision++
	if err = m.DB.Setting("proxy_config", c); err != nil {
		return c, err
	}
	c.Settings.DuckToken = ""
	return c, nil
}
func (m *Manager) Enqueue(action, id, actor string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy {
		return Job{}, errors.New("已有代理任务正在执行")
	}
	if action != "test" && action != "apply" && action != "stop" && action != "dns" && action != "certificate" {
		return Job{}, errors.New("不支持的代理操作")
	}
	c, err := m.load()
	if err != nil {
		return Job{}, err
	}
	if err = Normalize(&c); err != nil {
		return Job{}, err
	}
	var h Host
	if action == "certificate" {
		for _, v := range c.Hosts {
			if v.ID == id {
				h = v
			}
		}
		if h.ID == "" || !h.HTTPS {
			return Job{}, errors.New("请先保存规则并开启 HTTPS")
		}
		if err = m.validateIssue(h, c.Settings); err != nil {
			return Job{}, err
		}
	}
	if action == "dns" && (c.Settings.DuckDomain == "" || c.Settings.DuckToken == "") {
		return Job{}, errors.New("请先保存 DuckDNS 域名与 Token")
	}
	result, err := m.DB.Exec("INSERT INTO proxy_jobs(action,host_id) VALUES(?,?)", action, id)
	if err != nil {
		return Job{}, err
	}
	jid, err := result.LastInsertId()
	if err != nil {
		return Job{}, err
	}
	m.busy = true
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(m.ctx, 12*time.Minute)
		defer cancel()
		message, err := m.execute(ctx, action, h, c)
		status := "success"
		if err != nil {
			status = "failed"
			message = err.Error()
		}
		message = command.Redact(message)
		if len(message) > 8000 {
			message = message[len(message)-8000:]
		}
		_, _ = m.DB.Exec("UPDATE proxy_jobs SET status=?,message=?,finished_at=? WHERE id=?", status, message, database.Now(), jid)
		m.DB.Audit(actor, "proxy_"+action, id, status)
		m.mu.Lock()
		m.busy = false
		m.mu.Unlock()
	}()
	return Job{ID: jid, Action: action, HostID: id, Status: "running"}, nil
}
func (m *Manager) project() string {
	return fmt.Sprintf("cm-proxy-%x", sha256.Sum256([]byte(m.Dir)))[:25]
}
func (m *Manager) compose(ctx context.Context, path string, args ...string) (string, error) {
	a := []string{"compose", "--project-name", m.project(), "-f", path}
	a = append(a, args...)
	out, err := command.Capture(ctx, m.Runner, m.Dir, "docker", a...)
	if err != nil {
		return out, fmt.Errorf("Nginx 操作失败：%s %s", command.CleanError(err), out)
	}
	return out, nil
}
func (m *Manager) composeFile(s Settings, confDir string) ([]byte, error) {
	ports := []string{net.JoinHostPort(s.Bind, strconv.Itoa(s.HTTPPort)) + ":80", net.JoinHostPort(s.Bind, strconv.Itoa(s.HTTPSPort)) + ":443"}
	return yaml.Marshal(map[string]any{"services": map[string]any{"proxy": map[string]any{"image": "nginx:stable-alpine", "network_mode": "bridge", "restart": "unless-stopped", "ports": ports, "extra_hosts": []string{"host.docker.internal:host-gateway"}, "volumes": []any{map[string]any{"type": "bind", "source": confDir, "target": "/etc/nginx/cm", "read_only": true}, map[string]any{"type": "bind", "source": filepath.Join(m.Dir, "certs"), "target": "/etc/nginx/certs", "read_only": true}}, "command": []string{"nginx", "-c", "/etc/nginx/cm/nginx.conf", "-g", "daemon off;"}, "labels": map[string]string{"io.compose-manager.proxy": "true"}, "logging": map[string]any{"driver": "json-file", "options": map[string]string{"max-size": "10m", "max-file": "3"}}}}})
}
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func (m *Manager) execute(ctx context.Context, action string, h Host, c Config) (string, error) {
	switch action {
	case "dns":
		if err := m.duck(ctx, c.Settings, "", false); err != nil {
			return "", err
		}
		_ = m.DB.Setting("proxy_ddns_at", time.Now().Unix())
		return "DuckDNS IP 已更新；子域名使用同一地址，由 Nginx 分发到对应端口。", nil
	case "certificate":
		if err := m.issue(ctx, h, c.Settings); err != nil {
			return "", err
		}
		if err := m.reload(ctx); err != nil {
			return "证书已保存", err
		}
		return "证书已签发并保存；已运行的规则已重载，新规则请点击应用。", nil
	case "stop":
		if _, err := os.Stat(filepath.Join(m.Dir, "compose.yaml")); os.IsNotExist(err) {
			return "Nginx 尚未启动", nil
		}
		_, err := m.compose(ctx, filepath.Join(m.Dir, "compose.yaml"), "stop", "proxy")
		return "Nginx 已停止，保留代理规则与证书", err
	}
	for _, v := range c.Hosts {
		if v.Enabled && v.HTTPS {
			if _, err := m.certificate(v, c.Settings); err != nil {
				return "", fmt.Errorf("%s：没有有效证书，请先申请证书", v.Domain)
			}
		}
	}
	stage, err := os.MkdirTemp(m.Dir, "candidate-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err = atomicWrite(filepath.Join(stage, "nginx.conf"), []byte(Render(c))); err != nil {
		return "", err
	}
	candidate, err := m.composeFile(c.Settings, stage)
	if err != nil {
		return "", err
	}
	p := filepath.Join(stage, "compose.yaml")
	if err = atomicWrite(p, candidate); err != nil {
		return "", err
	}
	out, err := m.compose(ctx, p, "run", "--rm", "--no-deps", "proxy", "nginx", "-t", "-c", "/etc/nginx/cm/nginx.conf")
	if err != nil {
		return "", err
	}
	if action == "test" {
		return "Nginx 配置校验通过\n" + out, nil
	}
	conf := filepath.Join(m.Dir, "conf")
	if err = os.MkdirAll(conf, 0700); err != nil {
		return "", err
	}
	configPath := filepath.Join(conf, "nginx.conf")
	composePath := filepath.Join(m.Dir, "compose.yaml")
	oldConf, readConf := os.ReadFile(configPath)
	oldCompose, readCompose := os.ReadFile(composePath)
	final, err := m.composeFile(c.Settings, conf)
	if err != nil {
		return "", err
	}
	if err = atomicWrite(configPath, []byte(Render(c))); err != nil {
		return "", err
	}
	if err = atomicWrite(composePath, final); err != nil {
		if readConf == nil {
			_ = atomicWrite(configPath, oldConf)
		}
		return "", err
	}
	_, err = m.compose(ctx, composePath, "up", "-d", "--force-recreate", "--wait", "--wait-timeout", "30", "proxy")
	if err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		if readConf == nil && readCompose == nil {
			_ = atomicWrite(configPath, oldConf)
			_ = atomicWrite(composePath, oldCompose)
			_, rollbackErr := m.compose(rollbackCtx, composePath, "up", "-d", "--force-recreate", "--wait", "--wait-timeout", "30", "proxy")
			if rollbackErr != nil {
				return "", fmt.Errorf("%w；恢复旧配置失败：%v", err, rollbackErr)
			}
			return "", fmt.Errorf("%w；已恢复旧配置", err)
		}
		_, _ = m.compose(rollbackCtx, composePath, "down")
		_ = os.Remove(composePath)
		return "", err
	}
	if err = m.DB.Setting("proxy_active", c); err != nil {
		return "", fmt.Errorf("Nginx 已启动，但保存应用状态失败：%w", err)
	}
	return "配置校验通过，Nginx 已更新并启动。", nil
}
func (m *Manager) certificate(h Host, s Settings) (*x509.Certificate, error) {
	dir := filepath.Join(m.Dir, "certs", certName(h, s))
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem"))
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err = cert.VerifyHostname(h.Domain); err != nil {
		return nil, err
	}
	if time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
		return nil, errors.New("证书已失效")
	}
	return cert, nil
}
func (m *Manager) reload(ctx context.Context) error {
	p := filepath.Join(m.Dir, "compose.yaml")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil
	}
	out, err := m.compose(ctx, p, "ps", "--status", "running", "-q", "proxy")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	_, err = m.compose(ctx, p, "exec", "-T", "proxy", "nginx", "-t", "-c", "/etc/nginx/cm/nginx.conf")
	if err == nil {
		_, err = m.compose(ctx, p, "exec", "-T", "proxy", "nginx", "-s", "reload", "-c", "/etc/nginx/cm/nginx.conf")
	}
	return err
}
func (m *Manager) maintain() {
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return
	}
	c, err := m.load()
	m.mu.Unlock()
	if err != nil {
		return
	}
	// Renew only domains from the applied snapshot, never apply pending edits.
	active := Defaults()
	if m.DB.GetSetting("proxy_active", &active) != nil {
		return
	}
	for _, h := range active.Hosts {
		if !h.Enabled || !h.HTTPS {
			continue
		}
		found := false
		for _, v := range c.Hosts {
			if v.ID == h.ID && v.Domain == h.Domain && v.HTTPS {
				found = true
			}
		}
		if !found || active.Settings.Staging != c.Settings.Staging {
			continue
		}
		cert, e := m.certificate(h, c.Settings)
		due := e != nil
		if e == nil {
			window := cert.NotAfter.Sub(cert.NotBefore) / 3
			due = time.Until(cert.NotAfter) < window
		}
		if !due {
			continue
		}
		key := "proxy_renew_" + certName(h, c.Settings)
		var last int64
		_ = m.DB.GetSetting(key, &last)
		if time.Now().Unix()-last < 86400 {
			continue
		}
		if _, err := m.Enqueue("certificate", h.ID, "scheduler"); err == nil {
			_ = m.DB.Setting(key, time.Now().Unix())
		}
		return
	}
	var at int64
	_ = m.DB.GetSetting("proxy_ddns_attempt", &at)
	if c.Settings.DDNS && time.Now().Unix()-at >= 600 {
		if _, err := m.Enqueue("dns", "", "scheduler"); err == nil {
			_ = m.DB.Setting("proxy_ddns_attempt", time.Now().Unix())
		}
	}

}
