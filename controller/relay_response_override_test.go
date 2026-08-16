package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinalizeResponseOverrideSeparatesClientError verifies controller
// finalization discards the successful body without turning it into a relay error.
func TestFinalizeResponseOverrideSeparatesClientError(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: responseOverrideForControllerTest()},
	}
	relaycommon.StartResponseOverrideBuffer(c, info)
	c.Writer.Header().Set("Content-Length", "999")
	c.Writer.Header().Set("Content-Encoding", "gzip")
	c.Writer.Header().Set("ETag", `"upstream"`)
	c.Writer.WriteHeader(http.StatusOK)
	_, err := c.Writer.Write([]byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`))
	require.NoError(t, err)
	relaycommon.EvaluateResponseOverride(c, http.StatusOK)

	clientErr := finalizeResponseOverride(c, info)

	require.NotNil(t, clientErr)
	assert.Equal(t, http.StatusInternalServerError, clientErr.StatusCode)
	assert.Empty(t, recorder.Body.String())
	assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(c))
	writeRelayErrorResponse(c, types.RelayFormatOpenAI, nil, info, clientErr)
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "模型拒绝执行该指令")
	assert.Empty(t, recorder.Header().Get("Content-Encoding"))
	assert.Empty(t, recorder.Header().Get("ETag"))
	assert.NotEqual(t, "999", recorder.Header().Get("Content-Length"))
	assert.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
}

// TestFinalizeResponseOverrideUsesGeminiErrorEnvelope verifies native Gemini
// clients receive the Google Status schema after a response rule matches.
func TestFinalizeResponseOverrideUsesGeminiErrorEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/MODEL:generateContent", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:    types.RelayFormatGemini,
		RequestURLPath: c.Request.URL.Path,
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: map[string]interface{}{
			"operations": []interface{}{
				map[string]interface{}{
					"phase": "response",
					"mode":  "return_error",
					"value": map[string]interface{}{
						"message":     "模型拒绝执行该指令",
						"status_code": http.StatusForbidden,
						"code":        "response_rejected",
						"type":        "upstream_response_error",
					},
					"conditions": []interface{}{
						map[string]interface{}{
							"source": "semantic",
							"path":   "response.rejection_state",
							"mode":   "full",
							"value":  relaycommon.ResponseRejectionAll,
						},
					},
				},
			},
		}},
	}
	relaycommon.StartResponseOverrideBuffer(c, info)
	c.Writer.WriteHeader(http.StatusOK)
	_, err := c.Writer.Write([]byte(`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[]}`))
	require.NoError(t, err)
	relaycommon.EvaluateResponseOverride(c, http.StatusOK)

	clientErr := finalizeResponseOverride(c, info)

	require.NotNil(t, clientErr)
	writeRelayErrorResponse(c, types.RelayFormatGemini, nil, info, clientErr)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.JSONEq(t, `{
		"error": {
			"code": 403,
			"message": "模型拒绝执行该指令",
			"status": "PERMISSION_DENIED"
		}
	}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), `"type"`)
	assert.NotContains(t, recorder.Body.String(), `"param"`)
}

// TestFinalizeResponseOverrideCommitsUnmatchedCandidate verifies the ordinary
// successful path remains byte-for-byte transparent.
func TestFinalizeResponseOverrideCommitsUnmatchedCandidate(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: responseOverrideForControllerTest()},
	}
	relaycommon.StartResponseOverrideBuffer(c, info)
	body := []byte(`{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`)
	c.Writer.WriteHeader(http.StatusOK)
	_, err := c.Writer.Write(body)
	require.NoError(t, err)
	relaycommon.EvaluateResponseOverride(c, http.StatusOK)

	clientErr := finalizeResponseOverride(c, info)

	assert.Nil(t, clientErr)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, string(body), recorder.Body.String())
}

func responseOverrideForControllerTest() map[string]interface{} {
	return map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "response",
				"mode":  "return_error",
				"value": map[string]interface{}{
					"message":     "模型拒绝执行该指令",
					"status_code": http.StatusInternalServerError,
				},
				"conditions": []interface{}{
					map[string]interface{}{
						"source": "semantic",
						"path":   "response.primary_outcome",
						"mode":   "full",
						"value":  relaycommon.ResponseOutcomeRejected,
					},
				},
			},
		},
	}
}
