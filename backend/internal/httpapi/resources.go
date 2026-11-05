package httpapi

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) containerStats(c *gin.Context) {
	projects, err := s.DB.Stacks()
	if err != nil {
		fail(c, 500, "读取项目失败")
		return
	}
	names := make([]string, 0, len(projects))
	for _, project := range projects {
		names = append(names, project.ProjectName)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	stats, err := s.Docker.Stats(ctx, names)
	if err != nil {
		fail(c, 503, "资源采样暂不可用，请确认 Docker 正在运行并稍后刷新")
		return
	}
	c.JSON(200, gin.H{"data": stats})
}
