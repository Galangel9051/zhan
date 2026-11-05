// Package sshkey manages the SSH identity Zhan uses for Git operations.
//
// The private key lives in <data_dir>/ssh and never leaves the server through
// the HTTP API. A fresh installation gets a key automatically; the UI only
// exposes the public key and its fingerprint so the operator can authorize it
// on GitHub/GitLab/Gitee once.
package sshkey

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	privateName    = "id_ed25519"
	publicName     = "id_ed25519.pub"
	knownHostsName = "known_hosts"
)

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

type Manager struct {
	dir string
	mu  sync.Mutex
}

// Info is safe to return from the HTTP API. It never contains private key
// material.
type Info struct {
	PublicKey       string `json:"public_key"`
	Fingerprint     string `json:"fingerprint"`
	KeyPath         string `json:"key_path"`
	KnownHostsPath  string `json:"known_hosts_path"`
	CreatedAt       string `json:"created_at"`
	KnownHostsCount int    `json:"known_hosts_count"`
}

// TestResult describes one outbound SSH connection test.
type TestResult struct {
	OK      bool   `json:"ok"`
	Host    string `json:"host"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
	Output  string `json:"output"`
}

func New(dataDir string) *Manager {
	return &Manager{dir: filepath.Join(dataDir, "ssh")}
}

// Ensure creates the key on first use and repairs file permissions on every
// start. The bool reports whether a new key was generated.
func (m *Manager) Ensure() (Info, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.prepareLocked(); err != nil {
		return Info{}, false, err
	}
	if _, err := os.Stat(m.privatePath()); err == nil {
		info, err := m.infoLocked()
		return info, false, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return Info{}, false, err
	}
	if err := m.generateLocked(false); err != nil {
		return Info{}, false, err
	}
	info, err := m.infoLocked()
	return info, true, err
}

func (m *Manager) Info() (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.prepareLocked(); err != nil {
		return Info{}, err
	}
	return m.infoLocked()
}

// Regenerate replaces the key pair and keeps the old pair as .bak files. The
// known_hosts file is intentionally preserved so trust decisions are not
// silently reset.
func (m *Manager) Regenerate() (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.prepareLocked(); err != nil {
		return Info{}, err
	}
	if err := m.generateLocked(true); err != nil {
		return Info{}, err
	}
	return m.infoLocked()
}

// ResetKnownHosts clears all learned host fingerprints. The next connection
// will trust the host again (accept-new), which is useful after a legitimate
// host key rotation or a bad cached entry.
func (m *Manager) ResetKnownHosts() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.prepareLocked(); err != nil {
		return err
	}
	return os.WriteFile(m.knownHostsPath(), nil, 0600)
}

// Command returns the GIT_SSH_COMMAND used for every Git invocation.
func (m *Manager) Command() string {
	return fmt.Sprintf(
		"ssh -i %s -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=%s",
		shellQuote(m.privatePath()), shellQuote(m.knownHostsPath()),
	)
}

func (m *Manager) Test(ctx context.Context, host string) (TestResult, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		host = "github.com"
	}
	if !ValidHost(host) {
		return TestResult{}, errors.New("主机地址只能包含字母、数字、点和连字符")
	}
	if _, err := m.Info(); err != nil {
		return TestResult{}, err
	}
	args := []string{
		"-i", m.privatePath(),
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + m.knownHostsPath(),
		"-o", "ConnectTimeout=10",
		"-T", "git@" + host,
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	data, err := cmd.CombinedOutput()
	if len(data) > 4096 {
		data = data[:4096]
	}
	output := strings.TrimSpace(string(data))
	result := TestResult{Host: host, Output: output}
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "successfully authenticated"),
		strings.Contains(lower, "welcome to gitlab"),
		strings.Contains(lower, "you've successfully authenticated"):
		result.OK = true
		result.Message = "SSH 连接成功，远程平台已识别这把密钥。"
	case err == nil:
		result.OK = true
		result.Message = "SSH 连接成功。"
	default:
		result.Message = "SSH 连接失败。"
	}
	if !result.OK {
		result.Hint = connectionHint(lower, ctx.Err())
	}
	return result, nil
}

// ValidHost rejects option-like or malformed host names before they reach ssh.
func ValidHost(host string) bool {
	return len(host) <= 253 && hostPattern.MatchString(host) && !strings.HasPrefix(host, "-")
}

func connectionHint(output string, ctxErr error) string {
	switch {
	case strings.Contains(output, "permission denied (publickey)"):
		return "原因：远程仓库拒绝了 Zhan 的 SSH 公钥。\n解决：复制“设置 → Git 凭据”中的公钥，添加到 GitHub/GitLab/Gitee 的 SSH Keys 或仓库 Deploy Keys（只读即可），确认该密钥有仓库权限后再测试。"
	case strings.Contains(output, "host key verification failed"), strings.Contains(output, "remote host identification has changed"):
		return "原因：SSH 主机指纹未信任或已变化。\n解决：打开“设置 → Git 凭据”，先点“测试连接”记录指纹；如果提示指纹变化，确认平台公告后点“清除已信任主机”再重试。"
	case strings.Contains(output, "could not resolve hostname"):
		return "原因：容器内 DNS 无法解析该主机。\n解决：检查服务器网络和 DNS；如果是内网 Git 服务，确认容器能访问该地址。"
	case strings.Contains(output, "connection timed out"), strings.Contains(output, "connection refused"):
		return "原因：无法连接到主机的 22 端口，常见于防火墙或网络限制。\n解决：确认服务器可出站访问 22 端口；GitHub 可改用 ssh.github.com:443 或 HTTPS 仓库地址。"
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return "原因：连接测试超时。\n解决：检查服务器到该主机的网络和 22 端口；稍后重试。"
	default:
		return "原因：SSH 未能完成认证。\n解决：确认公钥已添加到对应平台，且仓库地址使用 SSH（git@host:owner/repo.git）；可展开下方原始输出查看详情。"
	}
}

func (m *Manager) prepareLocked() error {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return fmt.Errorf("创建 SSH 目录失败：%w", err)
	}
	if err := os.Chmod(m.dir, 0700); err != nil {
		return fmt.Errorf("修正 SSH 目录权限失败：%w", err)
	}
	if _, err := os.Stat(m.knownHostsPath()); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(m.knownHostsPath(), nil, 0600); err != nil {
			return fmt.Errorf("创建 known_hosts 失败：%w", err)
		}
	}
	return nil
}

func (m *Manager) generateLocked(backup bool) error {
	if backup {
		if _, err := os.Stat(m.privatePath()); err == nil {
			if err := os.Rename(m.privatePath(), m.privatePath()+".bak"); err != nil {
				return fmt.Errorf("备份旧私钥失败：%w", err)
			}
			if _, err := os.Stat(m.publicPath()); err == nil {
				_ = os.Rename(m.publicPath(), m.publicPath()+".bak")
			}
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("生成 SSH 密钥失败：%w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return fmt.Errorf("编码 SSH 私钥失败：%w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return fmt.Errorf("编码 SSH 公钥失败：%w", err)
	}
	privateTmp := m.privatePath() + ".tmp"
	publicTmp := m.publicPath() + ".tmp"
	if err = os.WriteFile(privateTmp, pem.EncodeToMemory(block), 0600); err != nil {
		return fmt.Errorf("写入 SSH 私钥失败：%w", err)
	}
	if err = os.WriteFile(publicTmp, ssh.MarshalAuthorizedKey(sshPub), 0644); err != nil {
		_ = os.Remove(privateTmp)
		return fmt.Errorf("写入 SSH 公钥失败：%w", err)
	}
	if err = os.Rename(privateTmp, m.privatePath()); err != nil {
		return fmt.Errorf("保存 SSH 私钥失败：%w", err)
	}
	if err = os.Rename(publicTmp, m.publicPath()); err != nil {
		return fmt.Errorf("保存 SSH 公钥失败：%w", err)
	}
	return m.repairPermissionsLocked()
}

func (m *Manager) repairPermissionsLocked() error {
	if err := os.Chmod(m.dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(m.privatePath(), 0600); err != nil {
		return err
	}
	_ = os.Chmod(m.publicPath(), 0644)
	_ = os.Chmod(m.knownHostsPath(), 0600)
	return nil
}

func (m *Manager) infoLocked() (Info, error) {
	if err := m.repairPermissionsLocked(); err != nil {
		return Info{}, err
	}
	publicBytes, err := os.ReadFile(m.publicPath())
	if err != nil {
		publicBytes, err = m.derivePublicLocked()
		if err != nil {
			return Info{}, err
		}
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey(publicBytes)
	if err != nil {
		return Info{}, fmt.Errorf("读取 SSH 公钥失败：%w", err)
	}
	stat, err := os.Stat(m.privatePath())
	if err != nil {
		return Info{}, err
	}
	info := Info{
		PublicKey:      strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))),
		Fingerprint:    ssh.FingerprintSHA256(publicKey),
		KeyPath:        m.privatePath(),
		KnownHostsPath: m.knownHostsPath(),
		CreatedAt:      stat.ModTime().UTC().Format(time.RFC3339),
	}
	if known, err := os.ReadFile(m.knownHostsPath()); err == nil {
		for _, line := range strings.Split(string(known), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				info.KnownHostsCount++
			}
		}
	}
	return info, nil
}

func (m *Manager) derivePublicLocked() ([]byte, error) {
	raw, err := os.ReadFile(m.privatePath())
	if err != nil {
		return nil, fmt.Errorf("读取 SSH 私钥失败：%w", err)
	}
	key, err := ssh.ParseRawPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 SSH 私钥失败：%w", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("解析 SSH 私钥失败：%w", err)
	}
	publicBytes := ssh.MarshalAuthorizedKey(signer.PublicKey())
	if err = os.WriteFile(m.publicPath(), publicBytes, 0644); err != nil {
		return nil, fmt.Errorf("写入 SSH 公钥失败：%w", err)
	}
	return publicBytes, nil
}

func (m *Manager) privatePath() string    { return filepath.Join(m.dir, privateName) }
func (m *Manager) publicPath() string     { return filepath.Join(m.dir, publicName) }
func (m *Manager) knownHostsPath() string { return filepath.Join(m.dir, knownHostsName) }

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// String is handy in logs and tests without exposing private material.
func (i Info) String() string {
	return fmt.Sprintf("ssh key %s (%s)", i.Fingerprint, i.KeyPath)
}
