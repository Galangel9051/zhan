package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type Runner interface {
	Run(context.Context, string, string, []string, io.Writer) error
}

// OSRunner executes host binaries. SSHCommand is the GIT_SSH_COMMAND used for
// Git operations; an empty value keeps the conservative legacy behavior.
type OSRunner struct {
	SSHCommand string
}
type Error struct {
	Code  int
	Cause error
}

func (e *Error) Error() string {
	return fmt.Sprintf("命令执行失败（退出码 %d）: %v", e.Code, e.Cause)
}
func (e *Error) Unwrap() error { return e.Cause }
func (r OSRunner) Run(ctx context.Context, dir, name string, args []string, out io.Writer) error {
	// The administrator selected this working tree. Trust only that directory for
	// this invocation: container root can differ from the host repository owner.
	if name == "git" && dir != "" {
		args = append([]string{"-c", "safe.directory=" + dir}, args...)
	}
	sshCommand := strings.TrimSpace(r.SSHCommand)
	if sshCommand == "" {
		sshCommand = "ssh -o BatchMode=yes -o StrictHostKeyChecking=yes"
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(withoutEnv(os.Environ(), "GIT_TERMINAL_PROMPT", "GIT_SSH_COMMAND", "GIT_CONFIG_NOSYSTEM", "COMPOSE_ANSI", "BUILDKIT_PROGRESS"),
		"GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND="+sshCommand, "GIT_CONFIG_NOSYSTEM=1", "COMPOSE_ANSI=never", "BUILDKIT_PROGRESS=plain")
	cmd.Stdout = out
	cmd.Stderr = out
	// Metadata commands must not mix Docker warnings on stderr into JSON stdout.
	var stderr *Buffer
	if _, metadata := out.(*Buffer); metadata {
		stderr = &Buffer{Limit: 64 * 1024}
		cmd.Stderr = stderr
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if stderr != nil {
		_, _ = out.Write(stderr.Data)
	}
	code := -1
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	return &Error{code, err}
}

type Buffer struct {
	Data     []byte
	Limit    int
	Overflow bool
}

func (b *Buffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.Limit - len(b.Data)
	if left > 0 {
		b.Data = append(b.Data, p[:min(left, n)]...)
	}
	if n > left {
		b.Overflow = true
	}
	return n, nil
}
func Capture(ctx context.Context, r Runner, dir, name string, args ...string) (string, error) {
	b := &Buffer{Limit: 4 * 1024 * 1024}
	err := r.Run(ctx, dir, name, args, b)
	if b.Overflow {
		return "", fmt.Errorf("命令输出超过元数据读取上限")
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, Redact(string(b.Data)))
	}
	return string(b.Data), nil
}

var secretURL = regexp.MustCompile(`(?i)(https?://)[^\s/@]+(?::[^\s/@]*)?@`)
var secretKV = regexp.MustCompile(`(?i)((?:password|passwd|token|secret|authorization|api[_-]?key)["']?\s*[:=]\s*["']?)[^\s"']+`)
var bearer = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`)

func withoutEnv(env []string, keys ...string) []string {
	prefixes := make([]string, 0, len(keys))
	for _, key := range keys {
		prefixes = append(prefixes, key+"=")
	}
	result := env[:0]
	for _, item := range env {
		keep := true
		for _, prefix := range prefixes {
			if strings.HasPrefix(item, prefix) {
				keep = false
				break
			}
		}
		if keep {
			result = append(result, item)
		}
	}
	return result
}

func Redact(s string) string {
	s = secretURL.ReplaceAllString(s, "${1}[REDACTED]@")
	s = secretKV.ReplaceAllString(s, "${1}[REDACTED]")
	return bearer.ReplaceAllString(s, "Bearer [REDACTED]")
}
func CleanError(err error) string {
	if err == nil {
		return ""
	}
	s := Redact(err.Error())
	if len(s) > 1500 {
		s = s[:1500]
	}
	return strings.TrimSpace(s)
}
