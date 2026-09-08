package middleware

import (
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"net/http"
)

// abortWithAuthPolicyRejection 记录已执行的权限拒绝分支，不改变鉴权或客户端错误。
func abortWithAuthPolicyRejection(c *gin.Context, reason string, message string, code ...types.ErrorCode) {
	// 原因来自固定调用点；不记录用户输入、令牌、IP 或请求正文。
	c.Set("auth_policy_rejection", reason)
	logger.LogInfo(c.Request.Context(), "relay_policy_rejection reason="+reason+" status=403")
	abortWithOpenAiMessage(c, http.StatusForbidden, message, code...)
}
