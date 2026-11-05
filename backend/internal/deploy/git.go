package deploy

import (
	"zhan/backend/internal/command"
	"zhan/backend/internal/model"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

func (e *Engine) git(ctx context.Context, s model.Stack, args ...string) (string, error) {
	return command.Capture(ctx, e.Docker.Runner, s.Directory, "git", args...)
}
func (e *Engine) ValidateGit(ctx context.Context, s *model.Stack) error {
	if s.Branch == "" {
		if s.Polling || s.AutoDeploy {
			return errors.New("启用轮询前必须绑定 Git 分支")
		}
		s.Repository = ""
		return nil
	}
	if strings.HasPrefix(s.Branch, "-") || strings.ContainsAny(s.Branch, "\r\n") {
		return errors.New("无效 Git 分支")
	}
	if _, err := e.git(ctx, *s, "check-ref-format", "--branch", s.Branch); err != nil {
		return errors.New("Git 分支格式无效")
	}
	root, err := e.git(ctx, *s, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(strings.TrimSpace(root))
	if err != nil || real != s.Directory {
		return errors.New("项目目录须为 Git 仓库根目录")
	}
	branch, err := e.git(ctx, *s, "symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) != s.Branch {
		return errors.New("工作区当前分支与绑定分支不一致，请先在服务器切换分支")
	}
	remote, err := e.git(ctx, *s, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	remote = strings.TrimSpace(remote)
	if strings.HasPrefix(remote, "-") || strings.ContainsAny(remote, "\r\n") {
		return errors.New("无效远程地址")
	}
	if strings.HasPrefix(remote, "http://") || strings.HasPrefix(remote, "https://") {
		u, err := url.Parse(remote)
		if err != nil || u.User != nil || u.RawQuery != "" {
			return errors.New("Git 地址不能包含密码或令牌，请使用宿主机 SSH Deploy Key 或凭据助手")
		}
	}
	if s.Repository != "" && s.Repository != remote {
		return errors.New("仓库地址与本地 origin 不一致；请在服务器管理 Git remote")
	}
	s.Repository = remote
	return nil
}
func (e *Engine) checkGit(ctx context.Context, s *model.Stack, out io.Writer) error {
	if s.Branch == "" {
		return errors.New("此项目尚未绑定 Git 仓库和分支")
	}
	if err := e.ValidateGit(ctx, s); err != nil {
		return err
	}
	local, err := e.git(ctx, *s, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	s.Local = strings.TrimSpace(local)
	remote, err := e.git(ctx, *s, "ls-remote", "--exit-code", "origin", "refs/heads/"+s.Branch)
	if err != nil {
		return err
	}
	fields := strings.Fields(remote)
	if len(fields) != 2 || !commitPattern.MatchString(fields[0]) {
		return errors.New("远程分支不存在或 Commit 无效")
	}
	s.Remote = fields[0]
	_, err = e.DB.Exec("UPDATE deployment_configs SET remote_commit=?,local_commit=? WHERE stack_id=?", s.Remote, s.Local, s.ID)
	fmt.Fprintf(out, "本地版本 %s\n远程版本 %s\n", s.Local, s.Remote)
	return err
}
func (e *Engine) pullGit(ctx context.Context, s *model.Stack, out io.Writer) error {
	if err := e.checkGit(ctx, s, out); err != nil {
		return err
	}
	dirty, err := e.git(ctx, *s, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if strings.TrimSpace(dirty) != "" {
		return errors.New("工作区存在本地修改或未跟踪文件，拒绝自动覆盖")
	}
	if err = e.Docker.Runner.Run(ctx, s.Directory, "git", []string{"fetch", "--no-tags", "origin", "refs/heads/" + s.Branch}, out); err != nil {
		return err
	}
	if err = e.Docker.Runner.Run(ctx, s.Directory, "git", []string{"merge", "--ff-only", "--no-overwrite-ignore", "FETCH_HEAD"}, out); err != nil {
		return err
	}
	local, err := e.git(ctx, *s, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	s.Local = strings.TrimSpace(local)
	if _, err = e.DB.Exec("UPDATE deployment_configs SET local_commit=? WHERE stack_id=?", s.Local, s.ID); err != nil {
		return err
	}
	// Ahead-only local commits are not silently published as the remote deployment.
	if s.Local != s.Remote {
		return errors.New("本地与检测到的远程版本不一致（本地超前或远程已变更），请检查后重试")
	}
	_, err = e.DB.Exec("UPDATE deployment_configs SET local_commit=? WHERE stack_id=?", s.Local, s.ID)
	return err
}
