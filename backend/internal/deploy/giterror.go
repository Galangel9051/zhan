package deploy

import (
	"strings"
)

// ExplainGitError prepends a short, actionable Chinese hint to common Git/SSH
// failures. The original error stays wrapped so callers can still inspect the
// exit code. The hint comes first so it survives the task error length limit.
func ExplainGitError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "原因：") && strings.Contains(msg, "解决：") {
		return err
	}
	hint := gitHint(strings.ToLower(msg))
	if hint == "" {
		return err
	}
	return &explainedError{cause: err, hint: hint}
}

type explainedError struct {
	cause error
	hint  string
}

func (e *explainedError) Error() string {
	return e.hint + "\n\n原始输出：\n" + e.cause.Error()
}

func (e *explainedError) Unwrap() error { return e.cause }

func gitHint(message string) string {
	contains := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(message, value) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("host key verification failed", "no ed25519 host key is known", "no rsa host key is known"):
		return "原因：SSH 无法确认远程主机身份，known_hosts 中没有该主机。\n解决：打开“设置 → Git 凭据”，点“测试连接”记录主机指纹；如果提示指纹已变化，请先核对平台公告，再清除已信任主机并重试。"
	case contains("remote host identification has changed"):
		return "原因：远程主机的 SSH 指纹与上次记录不一致，可能是平台更换了密钥，也可能是中间人攻击。\n解决：先在平台官网核对指纹；确认无误后打开“设置 → Git 凭据”清除已信任主机，再重新测试连接。"
	case contains("permission denied (publickey)"):
		return "原因：远程仓库拒绝了 Zhan 的 SSH 公钥。\n解决：复制“设置 → Git 凭据”中的公钥，添加到 GitHub/GitLab/Gitee 的 SSH Keys 或仓库 Deploy Keys（只读即可），确认密钥有该仓库权限后重新执行。"
	case contains("load key", "invalid format"):
		return "原因：SSH 私钥文件损坏或权限不正确。\n解决：打开“设置 → Git 凭据”重新生成密钥，并把新的公钥添加到 Git 平台。"
	case contains("no such file or directory") && contains("ssh"):
		return "原因：Zhan 的内置 SSH 密钥不存在或不可读。\n解决：打开“设置 → Git 凭据”重新生成密钥，然后把公钥添加到 Git 平台。"
	case contains("could not read username", "authentication failed", "terminal prompts disabled"):
		return "原因：这是 HTTPS 私有仓库，需要访问令牌，但 Zhan 没有可用的凭据。\n解决：把仓库 origin 改为 SSH 地址（git@host:owner/repo.git）并使用“设置 → Git 凭据”中的公钥；公开仓库的 HTTPS 地址可直接使用。"
	case contains("repository not found", "remote: not found", "404 not found"):
		return "原因：仓库不存在，或当前密钥/账号没有访问权限。\n解决：检查 origin 地址拼写；私有仓库请确认 Deploy Key 已添加且勾选了正确的仓库权限。"
	case contains("could not resolve host", "name or service not known", "temporary failure in name resolution"):
		return "原因：服务器无法解析 Git 主机名。\n解决：检查容器/服务器的 DNS 和网络；内网 Git 服务请确认地址在容器内可访问。"
	case contains("connection timed out", "connection refused", "network is unreachable", "connection reset"):
		return "原因：无法连接到 Git 主机的 SSH 端口（通常是 22），可能是防火墙或网络限制。\n解决：确认服务器可出站访问 22 端口；GitHub 也可以改用 HTTPS 公开地址或 ssh.github.com:443。"
	case contains("not a git repository"):
		return "原因：项目目录不是 Git 仓库根目录。\n解决：在“项目设置”中把根目录指向包含 .git 的仓库根目录，或在导入时选择正确目录。"
	case contains("detected dubious ownership"):
		return "原因：Git 认为工作区属主与当前进程不一致。\n解决：Zhan 会为本次命令自动设置 safe.directory；如果仍失败，请检查项目目录权限，或重新导入该项目。"
	case contains("工作区存在本地修改", "uncommitted changes", "would be overwritten by merge", "local changes"):
		return "原因：工作区有未提交的修改或未跟踪文件，Zhan 不会执行 reset --hard 覆盖它们。\n解决：在服务器上提交、暂存或撤销这些改动后再试；本地专用配置建议移到仓库外或加入 .gitignore。"
	case contains("couldn't find remote ref", "remote branch", "unknown revision"):
		return "原因：远程分支不存在，或分支名填写错误。\n解决：在“项目设置”中确认 Git 分支与远程一致，并先执行一次“检查更新”。"
	case contains("fatal: unable to access", "failed to connect"):
		return "原因：Git 无法访问远程地址，可能是网络、代理或地址错误。\n解决：检查服务器网络和 origin 地址；公开仓库可尝试改用 HTTPS。"
	case contains("fatal:", "error:", "ssh:"):
		return "原因：Git/SSH 命令执行失败。\n解决：查看任务日志中的完整输出；常见原因是仓库地址、Deploy Key 权限、分支不存在或网络不可达。"
	default:
		return ""
	}
}
