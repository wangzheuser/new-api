package middleware

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithFormatter(formatAccessLog))
}

// formatAccessLog 格式化访问日志，并跳过成功的健康检查。
func formatAccessLog(param gin.LogFormatterParams) string {
	if param.Path == "/api/status" && param.StatusCode >= http.StatusOK && param.StatusCode < http.StatusMultipleChoices {
		return ""
	}

	var requestID string
	if param.Keys != nil {
		requestID, _ = param.Keys[common.RequestIdKey].(string)
	}
	tag, _ := param.Keys[RouteTagKey].(string)
	if tag == "" {
		tag = "web"
	}
	return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
		param.TimeStamp.Format("2006/01/02 - 15:04:05"),
		tag,
		requestID,
		param.StatusCode,
		param.Latency,
		param.ClientIP,
		param.Method,
		param.Path,
	)
}
