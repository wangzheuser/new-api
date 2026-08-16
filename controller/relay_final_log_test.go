package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
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

// TestRecordRelayErrorLogPersistsIntermediateState protects final-only queries from in-flight retries.
func TestRecordRelayErrorLogPersistsIntermediateState(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	previousErrorLogEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		constant.ErrorLogEnabled = previousErrorLogEnabled
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 1)
	ctx.Set("username", "test-user")
	ctx.Set("original_model", "test-model")
	ctx.Set(common.RequestIdKey, "req-intermediate")

	relayError := types.NewOpenAIError(
		errors.New("upstream unavailable"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
	)
	recordRelayErrorLog(ctx, nil, relayError, "", nil, true)

	ctx.Set(common.RequestIdKey, "req-terminal")
	recordRelayErrorLog(ctx, nil, relayError, "", nil, false)

	var logs []model.Log
	require.NoError(t, db.Order("id").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.True(t, logs[0].IsIntermediate)
	require.False(t, logs[1].IsIntermediate)
}

// TestRecordRelayErrorLogPreservesDiscardedResponseOverrideAttempt verifies a
// failed attempt keeps its response-stage skip reason before retry state resets.
func TestRecordRelayErrorLogPreservesDiscardedResponseOverrideAttempt(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	previousErrorLogEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		constant.ErrorLogEnabled = previousErrorLogEnabled
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 1)
	ctx.Set("username", "test-user")
	ctx.Set("original_model", "test-model")
	ctx.Set(common.RequestIdKey, "req-response-override-attempt")
	decision := &relaycommon.ResponseOverrideDecision{
		Configured:       true,
		NotAppliedReason: relaycommon.ResponseOverrideNotAppliedRelayError,
		Billable:         true,
	}
	info := &relaycommon.RelayInfo{ResponseOverride: decision}
	relayError := types.NewOpenAIError(
		errors.New("upstream unavailable"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
	)

	recordRelayErrorLog(ctx, info, relayError, "", nil, true)

	var log model.Log
	require.NoError(t, db.First(&log).Error)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	responseOverride, ok := adminInfo["response_override"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, responseOverride["configured"])
	assert.Equal(t, relaycommon.ResponseOverrideNotAppliedRelayError, responseOverride["not_applied_reason"])
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
