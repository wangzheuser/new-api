package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestRelayRouterRegistersClaudeCountTokens 验证 Claude token 统计端点已注册。
func TestRelayRouterRegistersClaudeCountTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	found := false
	for _, route := range engine.Routes() {
		if route.Method == http.MethodPost && route.Path == "/v1/messages/count_tokens" {
			found = true
			break
		}
	}

	assert.True(t, found)
}
