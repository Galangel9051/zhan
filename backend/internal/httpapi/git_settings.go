package httpapi

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"zhan/backend/internal/command"
	"zhan/backend/internal/sshkey"
)

func (s *Server) gitSettings(c *gin.Context) {
	if s.Git == nil {
		fail(c, 503, "Git 凭据功能未初始化，请重启服务")
		return
	}
	info, err := s.Git.Info()
	if err != nil {
		fail(c, 500, "读取 Git 凭据失败："+command.CleanError(err))
		return
	}
	c.JSON(200, gin.H{"data": info})
}

func (s *Server) regenerateGitKey(c *gin.Context) {
	if s.Git == nil {
		fail(c, 503, "Git 凭据功能未初始化，请重启服务")
		return
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if c.Request.ContentLength != 0 {
		if !bind(c, &input) {
			return
		}
	}
	if !input.Confirm {
		fail(c, 400, "重新生成后栈长将改用新密钥，旧公钥需到 Git 平台手动撤销；请勾选确认后再操作")
		return
	}
	info, err := s.Git.Regenerate()
	s.DB.Audit(user(c).Username, "git_key_regenerate", "system", result(err))
	if err != nil {
		fail(c, 500, "重新生成 SSH 密钥失败："+command.CleanError(err))
		return
	}
	c.JSON(200, gin.H{"data": info})
}

func (s *Server) testGitKey(c *gin.Context) {
	if s.Git == nil {
		fail(c, 503, "Git 凭据功能未初始化，请重启服务")
		return
	}
	var input struct {
		Host string `json:"host"`
	}
	if c.Request.ContentLength != 0 {
		if !bind(c, &input) {
			return
		}
	}
	host := strings.ToLower(strings.TrimSpace(input.Host))
	if host == "" {
		host = "github.com"
	}
	if !sshkey.ValidHost(host) {
		fail(c, 400, "主机地址只能包含字母、数字、点和连字符")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	test, err := s.Git.Test(ctx, host)
	auditResult := "failed: authentication"
	if test.OK {
		auditResult = "success"
	}
	if err != nil {
		auditResult = result(err)
	}
	s.DB.Audit(user(c).Username, "git_key_test", host, auditResult)
	if err != nil {
		fail(c, 500, "测试 SSH 连接失败："+command.CleanError(err))
		return
	}
	c.JSON(200, gin.H{"data": test})
}

func (s *Server) resetGitKnownHosts(c *gin.Context) {
	if s.Git == nil {
		fail(c, 503, "Git 凭据功能未初始化，请重启服务")
		return
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if c.Request.ContentLength != 0 {
		if !bind(c, &input) {
			return
		}
	}
	if !input.Confirm {
		fail(c, 400, "清除已信任主机会让下次连接重新记录指纹，请勾选确认后再操作")
		return
	}
	err := s.Git.ResetKnownHosts()
	s.DB.Audit(user(c).Username, "git_known_hosts_reset", "system", result(err))
	if err != nil {
		fail(c, 500, "清除已信任主机失败："+command.CleanError(err))
		return
	}
	c.JSON(200, gin.H{"message": "已清除已信任主机，下次连接会重新记录指纹。"})
}
