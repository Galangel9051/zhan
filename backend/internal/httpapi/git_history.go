package httpapi

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"zhan/backend/internal/command"
	"zhan/backend/internal/model"
)

type gitCommit struct {
	Hash       string   `json:"hash"`
	Parents    []string `json:"parents"`
	Author     string   `json:"author"`
	AuthoredAt string   `json:"authored_at"`
	Subject    string   `json:"subject"`
}
type gitHistory struct {
	Commits      []gitCommit `json:"commits"`
	Local        string      `json:"local_commit"`
	Remote       string      `json:"remote_commit"`
	RemoteSource string      `json:"remote_source"`
	Branch       string      `json:"current_branch"`
	Dirty        bool        `json:"dirty"`
	Ahead        *int        `json:"ahead"`
	Behind       *int        `json:"behind"`
	Truncated    bool        `json:"truncated"`
}

var historyHash = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func (s *Server) gitHistory(c *gin.Context) {
	item, ok := s.getStack(c)
	if !ok {
		return
	}
	if item.Branch == "" {
		fail(c, 400, "请先在项目设置中绑定 Git 分支")
		return
	}
	dir, err := s.Docker.Allowed(item.Directory)
	if err != nil {
		fail(c, 400, "项目目录不在允许访问的范围内")
		return
	}
	item.Directory = dir
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	history, err := readGitHistory(ctx, s.Docker.Runner, item)
	if err != nil {
		fail(c, 400, "无法读取 Git 提交树，请检查仓库目录及权限后重试")
		return
	}
	c.JSON(200, gin.H{"data": history})
}

// Read only local objects and refs. Opening this view never fetches or changes HEAD.
func readGitHistory(ctx context.Context, runner command.Runner, item model.Stack) (gitHistory, error) {
	result := gitHistory{Commits: []gitCommit{}}
	git := func(args ...string) (string, error) {
		return command.Capture(ctx, runner, item.Directory, "git", args...)
	}
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return result, err
	}
	real, err := filepath.EvalSymlinks(strings.TrimSpace(root))
	if err != nil || real != item.Directory {
		return result, errors.New("项目目录须为仓库根目录")
	}
	if branch, err := git("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		result.Branch = strings.TrimSpace(branch)
	}
	// Resolve revision names to validated hashes before using them as log arguments.
	resolve := func(ref string) string {
		out, err := git("rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
		hash := strings.TrimSpace(out)
		if err != nil || !historyHash.MatchString(hash) {
			return ""
		}
		return hash
	}
	result.Local = resolve("HEAD")
	if result.Local == "" {
		return result, nil
	} // Unborn branch.
	dirty, err := git("--no-optional-locks", "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return result, err
	}
	result.Dirty = strings.TrimSpace(dirty) != ""
	result.Remote, result.RemoteSource = item.Remote, "checked"
	if result.Remote == "" {
		result.Remote = resolve("refs/remotes/origin/" + item.Branch)
		result.RemoteSource = "cached"
	}
	refs := []string{result.Local}
	for _, hash := range []string{result.Remote, item.Success, item.Attempted} {
		if historyHash.MatchString(hash) && resolve(hash) != "" {
			refs = append(refs, hash)
		}
	}
	if historyHash.MatchString(result.Remote) && resolve(result.Remote) != "" {
		counts, err := git("rev-list", "--left-right", "--count", result.Local+"..."+result.Remote, "--")
		if err != nil {
			return result, err
		}
		values := strings.Fields(counts)
		if len(values) != 2 {
			return result, errors.New("无效版本差异")
		}
		ahead, err := strconv.Atoi(values[0])
		if err != nil {
			return result, err
		}
		behind, err := strconv.Atoi(values[1])
		if err != nil {
			return result, err
		}
		result.Ahead, result.Behind = &ahead, &behind
	}
	args := []string{"log", "--topo-order", "--max-count=51", "--no-decorate", "--no-show-signature", "-z", "--format=%H%x00%P%x00%an%x00%aI%x00%s"}
	args = append(args, refs...)
	args = append(args, "--")
	output, err := git(args...)
	if err != nil {
		return result, err
	}
	fields := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	if len(fields)%5 != 0 {
		return result, errors.New("无效提交记录")
	}
	for i := 0; i < len(fields); i += 5 {
		if !historyHash.MatchString(fields[i]) {
			return result, errors.New("无效提交版本")
		}
		result.Commits = append(result.Commits, gitCommit{Hash: fields[i], Parents: strings.Fields(fields[i+1]), Author: fields[i+2], AuthoredAt: fields[i+3], Subject: fields[i+4]})
	}
	if len(result.Commits) > 50 {
		result.Commits = result.Commits[:50]
		result.Truncated = true
	}
	return result, nil
}
