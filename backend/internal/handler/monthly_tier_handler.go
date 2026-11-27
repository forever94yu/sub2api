package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"time"
)

func (h *UsageHandler) MonthlyTiers(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	result, err := h.usageService.MonthlyTierProgressForUser(c.Request.Context(), subject.UserID, time.Now())
	if err != nil {
		response.InternalError(c, "Monthly tier progress unavailable")
		return
	}
	response.Success(c, result)
}
