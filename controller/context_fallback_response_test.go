package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteContextFallbackResponseModels(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "json",
			input:    `{"id":"x","model":"MODEL_B"}`,
			expected: `{"id":"x","model":"MODEL_A"}`,
		},
		{
			name:     "responses event",
			input:    "data: {\"type\":\"response.created\",\"response\":{\"model\":\"MODEL_B\"}}\n\n",
			expected: "data: {\"type\":\"response.created\",\"response\":{\"model\":\"MODEL_A\"}}\n\n",
		},
		{
			name:     "done",
			input:    "data: [DONE]\n\n",
			expected: "data: [DONE]\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(rewriteContextFallbackResponseModels([]byte(tt.input), "MODEL_A")))
		})
	}
}

func TestContextFallbackResponseWriterRewritesSplitSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header("Content-Type", "text/event-stream")
	installContextFallbackResponseWriter(c, "MODEL_A")
	relaycommon.ApplyFinalResponseWriter(c)

	_, err := c.Writer.Write([]byte("data: {\"model\":\"MODE"))
	require.NoError(t, err)
	assert.Empty(t, recorder.Body.String())
	_, err = c.Writer.Write([]byte("L_B\"}\n\n"))
	require.NoError(t, err)

	assert.Equal(t, "data: {\"model\":\"MODEL_A\"}\n\n", recorder.Body.String())
}

// TestContextFallbackResponseWriterKeepsPartialSSEAfterBufferRelease protects
// transform state when an unexpected stream releases the inner buffer.
func TestContextFallbackResponseWriterKeepsPartialSSEAfterBufferRelease(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: map[string]interface{}{
			"operations": []interface{}{
				map[string]interface{}{
					"phase": "response",
					"mode":  "return_error",
					"value": map[string]interface{}{"message": "blocked"},
				},
			},
		}},
	}
	installContextFallbackResponseWriter(c, "MODEL_A")
	relaycommon.StartResponseOverrideBuffer(c, info)
	relaycommon.ApplyFinalResponseWriter(c)
	c.Header("Content-Type", "text/event-stream")

	writer, ok := c.Writer.(*contextFallbackResponseWriter)
	require.True(t, ok)
	_, err := writer.Write([]byte("data: {\"model\":\"MODEL_B\"}\n\ndata: {\"model\":\"MODE"))
	require.NoError(t, err)
	assert.Same(t, writer, c.Writer)
	assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(c))
	assert.Equal(t, "data: {\"model\":\"MODEL_A\"}\n\n", recorder.Body.String())

	_, err = c.Writer.Write([]byte("L_B\"}\n\n"))
	require.NoError(t, err)
	assert.Equal(t,
		"data: {\"model\":\"MODEL_A\"}\n\ndata: {\"model\":\"MODEL_A\"}\n\n",
		recorder.Body.String(),
	)
}

// TestContextFallbackResponseWriterDiscardsPartialCandidate verifies stale
// transformed bytes cannot prefix the replacement error body.
func TestContextFallbackResponseWriterDiscardsPartialCandidate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: map[string]interface{}{
			"operations": []interface{}{
				map[string]interface{}{
					"phase": "response",
					"mode":  "return_error",
					"value": map[string]interface{}{"message": "blocked"},
				},
			},
		}},
	}
	installContextFallbackResponseWriter(c, "MODEL_A")
	relaycommon.StartResponseOverrideBuffer(c, info)
	relaycommon.ApplyFinalResponseWriter(c)
	c.Header("Content-Type", "text/event-stream")

	_, err := c.Writer.Write([]byte("data: {\"model\":\"MODE"))
	require.NoError(t, err)
	assert.Empty(t, recorder.Body.String())
	buffer := relaycommon.CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	buffer.MarkRelayError()
	buffer.Discard(c)

	clientErr := types.WithOpenAIError(types.OpenAIError{Message: "upstream failed"}, http.StatusBadGateway)
	writeRelayErrorResponse(c, types.RelayFormatOpenAI, nil, info, clientErr)
	assert.NotContains(t, recorder.Body.String(), "MODE")
	assert.Contains(t, recorder.Body.String(), "upstream failed")
}

func TestContextFallbackResponseWriterDropsStaleContentLength(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header("Content-Type", "application/json")
	c.Header("Content-Length", "24")
	installContextFallbackResponseWriter(c, "MODEL_A_LONG")
	relaycommon.ApplyFinalResponseWriter(c)

	c.Writer.WriteHeader(200)
	_, err := c.Writer.Write([]byte(`{"model":"MODEL_B"}`))

	require.NoError(t, err)
	assert.Empty(t, recorder.Header().Get("Content-Length"))
	assert.JSONEq(t, `{"model":"MODEL_A_LONG"}`, recorder.Body.String())
}

// TestContextFallbackResponseOverrideUsesFinalClientBody verifies model
// restoration runs before response matching and client response capture.
func TestContextFallbackResponseOverrideUsesFinalClientBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousCaptureEnabled := common.ConversationCaptureEnabled
	common.ConversationCaptureEnabled = true
	t.Cleanup(func() {
		common.ConversationCaptureEnabled = previousCaptureEnabled
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "response",
				"mode":  "return_error",
				"value": map[string]interface{}{
					"status_code": http.StatusForbidden,
					"message":     "模型拒绝执行该指令",
				},
				"conditions": []interface{}{
					map[string]interface{}{
						"source": "body",
						"path":   "model",
						"mode":   "full",
						"value":  "MODEL_A",
					},
				},
			},
		},
	}
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, override)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{ConversationLogEnabled: true})

	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RequestedModelName: "MODEL_A",
		AttemptModelName:   "MODEL_B",
		RequestURLPath:     "/v1/chat/completions",
	}
	installContextFallbackResponseWriter(c, info.RequestedModelName)
	info.InitChannelMeta(c)

	buffer := relaycommon.CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	fallbackWriter, ok := c.Writer.(*contextFallbackResponseWriter)
	require.True(t, ok)
	assert.Same(t, buffer, fallbackWriter.ResponseWriter)

	providerBody := []byte(`{"model":"MODEL_B","choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`)
	info.MergeResponseSemantics(types.RelayFormatOpenAI, providerBody)
	c.Header("Content-Type", "application/json")
	c.Status(http.StatusOK)
	_, err := c.Writer.Write(providerBody)
	require.NoError(t, err)

	decision := relaycommon.EvaluateResponseOverride(c, http.StatusOK)
	require.NotNil(t, decision)
	require.True(t, decision.Applied)
	_, _, candidateBody := buffer.Snapshot()
	assert.JSONEq(t, `{"model":"MODEL_A","choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`, string(candidateBody))
	assert.Empty(t, info.ConversationCapture.Snapshot().ClientResponseBody)

	clientErr := finalizeResponseOverride(c, info)
	require.NotNil(t, clientErr)
	restoredWriter, ok := c.Writer.(*contextFallbackResponseWriter)
	require.True(t, ok)
	assert.NotSame(t, buffer, restoredWriter.ResponseWriter)
	assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(c))
	writeRelayErrorResponse(c, types.RelayFormatOpenAI, nil, info, clientErr)

	snapshot := info.ConversationCapture.Snapshot()
	assert.NotContains(t, string(snapshot.ClientResponseBody), `"model":"MODEL_B"`)
	assert.Contains(t, string(snapshot.ClientResponseBody), "模型拒绝执行该指令")
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.JSONEq(t, string(snapshot.ClientResponseBody), recorder.Body.String())
}
