package httpapi

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

func (s *Server) pagedTasks(c *gin.Context) {
	page, err := strconv.Atoi(c.Query("page"))
	if err != nil || page < 1 || page > 1000000000 {
		fail(c, 400, "页码须为 1–1000000000 的整数")
		return
	}
	size := 50
	if raw := c.Query("page_size"); c.Request.URL.Query().Has("page_size") {
		size, err = strconv.Atoi(raw)
		if err != nil || size < 1 || size > 200 {
			fail(c, 400, "每页条数须为 1–200 的整数")
			return
		}
	}
	status := c.DefaultQuery("status", "all")
	switch status {
	case "all", "running", "pending", "success", "failed", "cancelled":
	default:
		fail(c, 400, "无效任务状态")
		return
	}
	stack := int64(0)
	if raw := c.Query("stack_id"); c.Request.URL.Query().Has("stack_id") {
		stack, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || stack < 1 {
			fail(c, 400, "无效项目 ID")
			return
		}
	}
	if c.Query("before") != "" {
		fail(c, 400, "页码分页不能与 before 同时使用")
		return
	}
	items, pagination, err := s.DB.TaskPage(stack, status, page, size)
	if err != nil {
		fail(c, 500, "读取任务分页失败")
		return
	}
	c.JSON(200, gin.H{"data": items, "pagination": pagination})
}
