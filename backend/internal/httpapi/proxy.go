package httpapi

import (
	"zhan/backend/internal/proxy"
	"context"
	"github.com/gin-gonic/gin"
	"time"
)

func (s *Server) proxyRoutes(api *gin.RouterGroup) {
	group := api.Group("/proxy")
	group.Use(func(c *gin.Context) {
		if s.Proxy == nil {
			fail(c, 503, "反向代理管理尚未初始化")
			c.Abort()
			return
		}
		c.Next()
	})
	group.GET("", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		state, err := s.Proxy.State(ctx)
		respond(c, state, err)
	})
	group.PUT("/config", func(c *gin.Context) {
		var input proxy.Config
		if !bind(c, &input) {
			return
		}
		saved, err := s.Proxy.Save(input)
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		s.DB.Audit(user(c).Username, "proxy_save", "nginx", "success")
		c.JSON(200, gin.H{"data": saved})
	})
	group.POST("/jobs", func(c *gin.Context) {
		var input struct {
			Action string `json:"action"`
			HostID string `json:"host_id"`
		}
		if !bind(c, &input) {
			return
		}
		job, err := s.Proxy.Enqueue(input.Action, input.HostID, user(c).Username)
		if err != nil {
			fail(c, 409, err.Error())
			return
		}
		c.JSON(202, gin.H{"data": job})
	})
}
