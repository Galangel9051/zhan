package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"zhan/backend/internal/command"
)

type gitBranches struct {
	IsRepository  bool     `json:"is_repository"`
	CurrentBranch string   `json:"current_branch"`
	Branches      []string `json:"branches"`
	HasOrigin     bool     `json:"has_origin"`
}

// Inspect only local metadata; do not fetch, checkout, or expose remote URLs.
func (s *Server) inspectGitBranches(c *gin.Context) {
	dir, err := s.Docker.Allowed(c.Query("path"))
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		fail(c, 400, "请选择项目根目录")
		return
	}
	result := gitBranches{Branches: []string{}}
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		c.JSON(200, gin.H{"data": result})
		return
	} else if err != nil {
		fail(c, 400, "无法读取 Git 信息，请检查目录权限")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	git := func(args ...string) (string, error) {
		return command.Capture(ctx, s.Docker.Runner, dir, "git", args...)
	}
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		fail(c, 400, "无法读取 Git 仓库，请检查 Git 安装及目录权限")
		return
	}
	real, err := filepath.EvalSymlinks(strings.TrimSpace(root))
	if err != nil || real != dir {
		fail(c, 400, "项目目录须为 Git 仓库根目录")
		return
	}
	refs, err := git("for-each-ref", "--format=%(refname)", "refs/heads/", "refs/remotes/origin/")
	if err != nil {
		fail(c, 400, "读取 Git 分支失败，请稍后重试")
		return
	}
	result.IsRepository = true
	if branch, err := git("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		result.CurrentBranch = strings.TrimSpace(branch)
	}
	_, err = git("remote", "get-url", "origin")
	result.HasOrigin = err == nil
	seen := map[string]bool{}
	for _, ref := range strings.Fields(refs) {
		var branch string
		if strings.HasPrefix(ref, "refs/heads/") {
			branch = strings.TrimPrefix(ref, "refs/heads/")
		} else if strings.HasPrefix(ref, "refs/remotes/origin/") && ref != "refs/remotes/origin/HEAD" {
			branch = strings.TrimPrefix(ref, "refs/remotes/origin/")
		}
		if branch != "" {
			seen[branch] = true
		}
	}
	if result.CurrentBranch != "" {
		seen[result.CurrentBranch] = true
	}
	for branch := range seen {
		result.Branches = append(result.Branches, branch)
	}
	sort.Strings(result.Branches)
	c.JSON(200, gin.H{"data": result})
}
