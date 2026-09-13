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
	"github.com/stretchr/testify/assert"
)

// TestLocalTruncationErrorsUseSafeMessageWithoutOverride prevents internal truncation details from reaching clients.
func TestLocalTruncationErrorsUseSafeMessageWithoutOverride(t *testing.T) {
	for _, code := range []string{"context_length_exceeded", "context_truncation_unsupported_content: multimodal", "context_truncation_final_budget_exceeded"} {
		t.Run(code, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			local := types.NewErrorWithStatusCode(errors.New(code), types.ErrorCode(code), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			info := &relaycommon.RelayInfo{LastError: local, ContextTruncation: &relaycommon.ContextTruncationState{Enabled: true}}
			result := resolveConfiguredFinalRelayError(c, info)
			assert.NotSame(t, local, result)
			assert.Equal(t, http.StatusBadRequest, result.StatusCode)
			assert.Equal(t, types.ErrorCode(code), result.GetErrorCode())
			assert.Equal(t, "请求上下文过长，请减少输入内容后重试。", result.Error())
			// The same text from an upstream response must still use the existing final-error rules.
			upstream := types.NewErrorWithStatusCode(errors.New(code), types.ErrorCode(code), http.StatusBadRequest, types.ErrOptionWithUpstreamStatusCode(http.StatusBadRequest))
			info.LastError = upstream
			withPolicy := resolveConfiguredFinalRelayError(c, info)
			info.ContextTruncation = nil
			withoutPolicy := resolveConfiguredFinalRelayError(c, info)
			assert.Equal(t, withoutPolicy.StatusCode, withPolicy.StatusCode)
			assert.Equal(t, withoutPolicy.GetErrorCode(), withPolicy.GetErrorCode())
			assert.Equal(t, withoutPolicy.Error(), withPolicy.Error())
		})
	}
}

// TestLocalTruncationErrorsApplyChannelOverride verifies local errors can use channel final_error rules.
func TestLocalTruncationErrorsApplyChannelOverride(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	local := types.NewErrorWithStatusCode(errors.New("context_length_exceeded"), types.ErrorCode("context_length_exceeded"), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	info := &relaycommon.RelayInfo{
		LastError:         local,
		ContextTruncation: &relaycommon.ContextTruncationState{Enabled: true},
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: map[string]interface{}{
			"operations": []interface{}{map[string]interface{}{
				"phase": "final_error",
				"mode":  "return_error",
				"value": map[string]interface{}{
					"message":     "自定义上下文错误",
					"status_code": 500,
					"code":        "custom_context_error",
				},
			}},
		}},
	}

	result := resolveConfiguredFinalRelayError(c, info)
	assert.Equal(t, http.StatusInternalServerError, result.StatusCode)
	assert.Equal(t, types.ErrorCode("custom_context_error"), result.GetErrorCode())
	assert.Equal(t, "自定义上下文错误", result.ToOpenAIError().Message)
}

// TestLocalTruncationErrorsApplyGlobalOverride verifies the global final_error fallback is also evaluated.
func TestLocalTruncationErrorsApplyGlobalOverride(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	original := settings.DefaultFinalErrorOverride
	t.Cleanup(func() { settings.DefaultFinalErrorOverride = original })
	settings.DefaultFinalErrorOverride = map[string]interface{}{
		"operations": []interface{}{map[string]interface{}{
			"phase": "final_error",
			"mode":  "return_error",
			"value": map[string]interface{}{
				"message":     "全局上下文错误",
				"status_code": 500,
				"code":        "global_context_error",
			},
		}},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	local := types.NewErrorWithStatusCode(errors.New("context_truncation_final_budget_exceeded"), types.ErrorCode("context_truncation_final_budget_exceeded"), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	info := &relaycommon.RelayInfo{LastError: local, ContextTruncation: &relaycommon.ContextTruncationState{Enabled: true}}

	result := resolveConfiguredFinalRelayError(c, info)
	assert.Equal(t, http.StatusInternalServerError, result.StatusCode)
	assert.Equal(t, types.ErrorCode("global_context_error"), result.GetErrorCode())
	assert.Equal(t, "全局上下文错误", result.ToOpenAIError().Message)
}
