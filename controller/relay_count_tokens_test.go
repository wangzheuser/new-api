package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCountTokensReturnsLocalClaudeTokenEstimate 验证显式统计不依赖计费 token 开关。
func TestCountTokensReturnsLocalClaudeTokenEstimate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := constant.CountToken
	constant.CountToken = false
	t.Cleanup(func() { constant.CountToken = previous })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/messages/count_tokens?beta=true",
		strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "gpt-4o-mini")

	CountTokens(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		InputTokens int `json:"input_tokens"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Positive(t, response.InputTokens)
}

// TestShouldRetrySkipsToolProtocolInvalidOnly 验证确定性协议错误不影响普通 502 重试。
func TestShouldRetrySkipsToolProtocolInvalidOnly(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	protocolError := types.WithOpenAIError(types.OpenAIError{
		Message: "invalid tool call",
		Code:    "tool_protocol_invalid",
	}, http.StatusBadGateway)
	ordinaryError := types.WithOpenAIError(types.OpenAIError{
		Message: "temporary upstream failure",
		Code:    "upstream_error",
	}, http.StatusBadGateway)

	assert.False(t, shouldRetry(ctx, protocolError, 1))
	assert.True(t, shouldRetry(ctx, ordinaryError, 1))
}
