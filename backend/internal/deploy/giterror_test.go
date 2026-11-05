package deploy

import (
	"errors"
	"strings"
	"testing"

	"zhan/backend/internal/command"
)

func TestExplainGitErrorAddsActionableHint(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{"host key", "Host key verification failed.", "known_hosts"},
		{"public key", "git@github.com: Permission denied (publickey).", "Deploy Keys"},
		{"https private", "fatal: could not read Username for 'https://github.com': terminal prompts disabled", "SSH 地址"},
		{"dns", "ssh: Could not resolve hostname github.com: Name or service not known", "DNS"},
		{"network", "ssh: connect to host github.com port 22: Connection timed out", "22 端口"},
		{"dirty", "工作区存在本地修改或未跟踪文件，拒绝自动覆盖", "reset --hard"},
		{"branch", "fatal: couldn't find remote ref refs/heads/missing", "远程分支"},
		{"not repo", "fatal: not a git repository (or any of the parent directories): .git", "Git 仓库根目录"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			explained := ExplainGitError(errors.New(item.msg))
			if !strings.Contains(explained.Error(), item.want) {
				t.Fatalf("hint missing %q:\n%s", item.want, explained)
			}
			if !strings.Contains(explained.Error(), "原因：") || !strings.Contains(explained.Error(), "解决：") {
				t.Fatalf("hint is not actionable:\n%s", explained)
			}
		})
	}
}

func TestExplainGitErrorKeepsExitCode(t *testing.T) {
	original := &command.Error{Code: 128, Cause: errors.New("Host key verification failed")}
	explained := ExplainGitError(original)
	var exit *command.Error
	if !errors.As(explained, &exit) {
		t.Fatalf("exit code lost: %v", explained)
	}
	if exit.Code != 128 {
		t.Fatalf("exit code = %d", exit.Code)
	}
}

func TestExplainGitErrorLeavesNonGitErrorsAlone(t *testing.T) {
	original := errors.New("Git 分支格式无效")
	explained := ExplainGitError(original)
	if explained.Error() != original.Error() {
		t.Fatalf("unexpected hint added: %s", explained)
	}
}

func TestExplainGitErrorIsIdempotent(t *testing.T) {
	first := ExplainGitError(errors.New("Host key verification failed"))
	second := ExplainGitError(first)
	if strings.Count(second.Error(), "原因：") != 1 {
		t.Fatalf("hint applied twice:\n%s", second)
	}
}
