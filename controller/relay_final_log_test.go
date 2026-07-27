package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestRelayClientErrorLogContent verifies that final logs use the structured client-facing error.
func TestRelayClientErrorLogContent(t *testing.T) {
	t.Run("OpenAI structured error", func(t *testing.T) {
		err := types.WithOpenAIError(types.OpenAIError{
			Message: "最终响应",
			Type:    "upstream_error",
			Code:    "upstream_error",
		}, http.StatusServiceUnavailable)
		err.SetMessage("internal wrapper (request id: req-test)")

		require.Equal(t, "status_code=503, 最终响应", relayClientErrorLogContent(err, types.RelayFormatOpenAI))
	})

	t.Run("Claude structured error", func(t *testing.T) {
		err := types.WithClaudeError(types.ClaudeError{
			Message: "最终响应",
			Type:    "upstream_error",
		}, http.StatusServiceUnavailable)
		err.SetMessage("internal wrapper (request id: req-test)")

		require.Equal(t, "status_code=503, 最终响应", relayClientErrorLogContent(err, types.RelayFormatClaude))
	})

	t.Run("local error includes request id", func(t *testing.T) {
		err := types.NewErrorWithStatusCode(errors.New("最终响应"), types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)
		err.SetMessage("最终响应 (request id: req-test)")

		require.Equal(
			t,
			"status_code=503, 最终响应 (request id: req-test)",
			relayClientErrorLogContent(err, types.RelayFormatOpenAI),
		)
	})
}

// TestResolveConfiguredFinalRelayError verifies channel precedence, system fallback, and leak-safe fallback.
func TestResolveConfiguredFinalRelayError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	settings := operation_setting.GetGeneralSetting()
	previous := settings.DefaultFinalErrorOverride
	t.Cleanup(func() {
		settings.DefaultFinalErrorOverride = previous
	})
	settings.DefaultFinalErrorOverride = finalErrorOverrideForTest("系统错误", 503, "default_error")

	relayInfo := &relaycommon.RelayInfo{
		LastError: types.WithOpenAIError(types.OpenAIError{
			Message: "raw upstream error",
			Code:    "upstream_error",
		}, http.StatusServiceUnavailable),
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: finalErrorOverrideForTest("渠道错误", 502, "channel_error"),
		},
	}

	mapped := resolveConfiguredFinalRelayError(ctx, relayInfo)
	require.Equal(t, http.StatusBadGateway, mapped.StatusCode)
	require.Equal(t, types.ErrorCode("channel_error"), mapped.GetErrorCode())
	require.Equal(t, "渠道错误", mapped.ToOpenAIError().Message)

	relayInfo.ChannelMeta.ParamOverride = nil
	mapped = resolveConfiguredFinalRelayError(ctx, relayInfo)
	require.Equal(t, http.StatusServiceUnavailable, mapped.StatusCode)
	require.Equal(t, types.ErrorCode("default_error"), mapped.GetErrorCode())
	require.Equal(t, "系统错误", mapped.ToOpenAIError().Message)

	settings.DefaultFinalErrorOverride = nil
	mapped = resolveConfiguredFinalRelayError(ctx, relayInfo)
	require.Equal(t, http.StatusBadGateway, mapped.StatusCode)
	require.Equal(t, types.ErrorCodeBadResponse, mapped.GetErrorCode())
	require.Equal(t, http.StatusText(http.StatusBadGateway), mapped.ToOpenAIError().Message)
}

func finalErrorOverrideForTest(message string, statusCode int, code string) map[string]interface{} {
	return map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "final_error",
				"mode":  "return_error",
				"value": map[string]interface{}{
					"message":     message,
					"status_code": statusCode,
					"code":        code,
					"type":        "new_api_error",
				},
			},
		},
	}
}
