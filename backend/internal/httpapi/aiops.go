package httpapi

import (
	"context"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"zhan/backend/internal/aiops"
)

func (s *Server) aiRoutes(api *gin.RouterGroup) {
	ai := api.Group("/ai")
	ai.Use(func(c *gin.Context) {
		if s.AI == nil {
			fail(c, 503, "AI 服务未启动")
			c.Abort()
			return
		}
		c.Next()
	})
	ai.GET("/settings", func(c *gin.Context) { v, e := s.AI.Settings(false); respond(c, v, e) })
	ai.PUT("/settings", func(c *gin.Context) {
		var input aiops.Settings
		if !bind(c, &input) {
			return
		}
		err := s.AI.SaveSettings(input)
		s.DB.Audit(user(c).Username, "ai_settings", "system", result(err))
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		v, e := s.AI.Settings(false)
		respond(c, v, e)
	})
	ai.POST("/models/discover", func(c *gin.Context) {
		var input aiops.Settings
		if !bind(c, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		models, err := s.AI.DiscoverModels(ctx, input)
		s.DB.Audit(user(c).Username, "ai_models_discover", "system", result(err))
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		c.JSON(200, gin.H{"data": models})
	})
	ai.GET("/jobs", func(c *gin.Context) { v, e := s.AI.Jobs(); respond(c, v, e) })
	save := func(c *gin.Context) {
		var input aiops.Job
		if !bind(c, &input) {
			return
		}
		input.ID = 0
		if c.Param("id") != "" {
			id, ok := idParam(c)
			if !ok {
				return
			}
			input.ID = id
		}
		err := s.AI.SaveJob(&input)
		s.DB.Audit(user(c).Username, "ai_job_save", input.Name, result(err))
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		c.JSON(200, gin.H{"data": input})
	}
	ai.POST("/jobs", save)
	ai.PUT("/jobs/:id", save)
	ai.POST("/jobs/:id/run", func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		runID, err := s.AI.Enqueue(id)
		s.DB.Audit(user(c).Username, "ai_run", strconv.FormatInt(id, 10), result(err))
		if err != nil {
			fail(c, 409, err.Error())
			return
		}
		v, e := s.AI.Run(runID)
		if e != nil {
			fail(c, 500, "任务已排队，读取失败")
			return
		}
		c.JSON(202, gin.H{"data": v})
	})
	ai.GET("/runs", func(c *gin.Context) {
		before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
		if err != nil || before < 0 {
			fail(c, 400, "无效游标")
			return
		}
		v, e := s.AI.Runs(before)
		respond(c, v, e)
	})
	ai.GET("/runs/:id", func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		v, e := s.AI.Run(id)
		respond(c, v, e)
	})
	ai.POST("/runs/:id/cancel", func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		err := s.AI.Cancel(id)
		s.DB.Audit(user(c).Username, "ai_cancel", strconv.FormatInt(id, 10), result(err))
		if err != nil {
			fail(c, 409, err.Error())
			return
		}
		c.Status(204)
	})
	ai.POST("/events", func(c *gin.Context) {
		var input struct {
			Name    string `json:"name"`
			StackID int64  `json:"stack_id"`
			Payload string `json:"payload"`
		}
		if !bind(c, &input) {
			return
		}
		err := s.AI.Emit(input.Name, input.StackID, input.Payload)
		s.DB.Audit(user(c).Username, "ai_event", input.Name, result(err))
		if err != nil {
			fail(c, 400, err.Error())
			return
		}
		c.Status(204)
	})
}
