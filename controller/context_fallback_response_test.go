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

func TestRewriteRequestedModelResponse(t *testing.T) {
	info := &relaycommon.RelayInfo{RequestedModelName: "MODEL_A"}
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
			name:     "preserve sse fields and spacing",
			input:    ": comment\r\nevent: response.created\r\nid: 7\r\ndata:\t{\"response\":{\"modelVersion\":\"MODEL_B\"}}  \r\n\r\n",
			expected: ": comment\r\nevent: response.created\r\nid: 7\r\ndata:\t{\"response\":{\"modelVersion\":\"MODEL_A\"}}  \r\n\r\n",
		},
		{
			name:     "done and comment",
			input:    ": ping\n\ndata: [DONE]\n\n",
			expected: ": ping\n\ndata: [DONE]\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, _ := rewriteRequestedModelResponse([]byte(tt.input), info, http.StatusOK)
			assert.Equal(t, tt.expected, string(actual))
		})
	}
}

func TestRequestedModelResponseWriterRewritesSplitSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header("Content-Type", "text/event-stream")
	info := &relaycommon.RelayInfo{RequestedModelName: "MODEL_A"}
	installRequestedModelResponseWriter(c, info)
	relaycommon.ApplyFinalResponseWriter(c)

	_, err := c.Writer.Write([]byte("data: {\"model\":\"MODE"))
	require.NoError(t, err)
	assert.Empty(t, recorder.Body.String())
	_, err = c.Writer.Write([]byte("L_B\"}\n\n"))
	require.NoError(t, err)

	assert.Equal(t, "data: {\"model\":\"MODEL_A\"}\n\n", recorder.Body.String())
}

func TestRequestedModelResponseWriterRewritesSplitJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{RequestedModelName: "MODEL_A"}
	installRequestedModelResponseWriter(c, info)
	relaycommon.ApplyFinalResponseWriter(c)

	_, err := c.Writer.Write([]byte(`{"model":"MODE`))
	require.NoError(t, err)
	assert.Empty(t, recorder.Body.String())
	_, err = c.Writer.Write([]byte(`L_B","choices":[]}`))
	require.NoError(t, err)

	assert.JSONEq(t, `{"model":"MODEL_A","choices":[]}`, recorder.Body.String())
}

func TestRequestedModelResponseWriterPreservesInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{RequestedModelName: "MODEL_A"}
	installRequestedModelResponseWriter(c, info)
	relaycommon.ApplyFinalResponseWriter(c)

	input := []byte(`{"model":"MODEL_B"`)
	_, err := c.Writer.Write(input)
	require.NoError(t, err)
	assert.Empty(t, recorder.Body.String())
	require.NoError(t, relaycommon.FinishFinalResponseWriter(c, true))

	assert.Equal(t, input, recorder.Body.Bytes())
}

// TestRequestedModelResponseWriterKeepsPartialSSEAfterBufferRelease protects
// transform state when an unexpected stream releases the inner buffer.
func TestRequestedModelResponseWriterKeepsPartialSSEAfterBufferRelease(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := responseWriterOverrideRelayInfo()
	installRequestedModelResponseWriter(c, info)
	relaycommon.StartResponseOverrideBuffer(c, info)
	relaycommon.ApplyFinalResponseWriter(c)
	c.Header("Content-Type", "text/event-stream")

	writer, ok := c.Writer.(*requestedModelResponseWriter)
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

// TestRequestedModelResponseWriterDiscardsPartialCandidate verifies stale
// bytes from a failed attempt cannot prefix the replacement error body.
func TestRequestedModelResponseWriterDiscardsPartialCandidate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := responseWriterOverrideRelayInfo()
	installRequestedModelResponseWriter(c, info)
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

	clientErr := types.WithOpenAIError(types.OpenAIError{Message: "MODEL_B failed"}, http.StatusBadGateway)
	writeRelayErrorResponse(c, types.RelayFormatOpenAI, nil, info, clientErr)
	assert.NotContains(t, recorder.Body.String(), "MODEL_B")
	assert.Contains(t, recorder.Body.String(), "MODEL_A failed")
}

func TestRequestedModelResponseWriterDiscardsPartialCandidateWithoutOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A",
		AttemptModelName:   "MODEL_B",
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_B"},
	}
	c.Header("Content-Type", "text/event-stream")
	installRequestedModelResponseWriter(c, info)
	relaycommon.ApplyFinalResponseWriter(c)

	_, err := c.Writer.Write([]byte("data: {\"model\":\"MODE"))
	require.NoError(t, err)
	assert.Empty(t, recorder.Body.String())
	require.NoError(t, relaycommon.FinishFinalResponseWriter(c, false))

	c.Header("Content-Type", "application/json")
	clientErr := types.WithOpenAIError(types.OpenAIError{Message: "MODEL_B failed"}, http.StatusBadGateway)
	writeRelayErrorResponse(c, types.RelayFormatOpenAI, nil, info, clientErr)
	assert.NotContains(t, recorder.Body.String(), "MODEL_B")
	assert.Contains(t, recorder.Body.String(), "MODEL_A failed")
}

func TestRequestedModelResponseWriterFiltersHeadersAfterTransformation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header("Content-Type", "application/json")
	c.Header("Content-Length", "24")
	c.Header("Content-Encoding", "gzip")
	c.Header("Content-Range", "bytes 0-23/24")
	c.Header("ETag", `"entity"`)
	c.Header("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
	c.Header("Content-MD5", "digest")
	c.Header("Digest", "sha-256=digest")
	c.Header("X-Upstream-Model", "MODEL_B")
	c.Header("X-Deployment", "region/MODEL_B-build")
	c.Header("X-Request-Id", "request-id")
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A_LONG",
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_B"},
	}
	installRequestedModelResponseWriter(c, info)
	relaycommon.ApplyFinalResponseWriter(c)

	c.Writer.WriteHeader(http.StatusOK)
	_, err := c.Writer.Write([]byte(`{"model":"MODEL_B"}`))

	require.NoError(t, err)
	for _, key := range []string{
		"Content-Length", "Content-Encoding", "Content-Range", "ETag", "Last-Modified", "Content-MD5", "Digest",
		"X-Upstream-Model", "X-Deployment",
	} {
		assert.Empty(t, recorder.Header().Get(key), key)
	}
	assert.Equal(t, "request-id", recorder.Header().Get("X-Request-Id"))
	assert.JSONEq(t, `{"model":"MODEL_A_LONG"}`, recorder.Body.String())
}

func TestRequestedModelResponseWriterPreservesNonJSONBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte{0x00, 0x01, 0x02, 0xff}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", "4")
	c.Header("X-Upstream-Model", "MODEL_B")
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A",
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_B"},
	}
	installRequestedModelResponseWriter(c, info)
	relaycommon.ApplyFinalResponseWriter(c)

	_, err := c.Writer.Write(body)

	require.NoError(t, err)
	assert.Equal(t, body, recorder.Body.Bytes())
	assert.Equal(t, "4", recorder.Header().Get("Content-Length"))
	assert.Empty(t, recorder.Header().Get("X-Upstream-Model"))
}

// TestRequestedModelResponseOverrideUsesFinalClientBody verifies model
// restoration runs before response matching and client response capture.
func TestRequestedModelResponseOverrideUsesFinalClientBody(t *testing.T) {
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
	installRequestedModelResponseWriter(c, info)
	info.InitChannelMeta(c)

	buffer := relaycommon.CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	responseWriter, ok := c.Writer.(*requestedModelResponseWriter)
	require.True(t, ok)
	assert.Same(t, buffer, responseWriter.ResponseWriter)

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
	restoredWriter, ok := c.Writer.(*requestedModelResponseWriter)
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

// responseWriterOverrideRelayInfo creates a relay fixture with one response override operation.
func responseWriterOverrideRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RequestedModelName: "MODEL_A",
		AttemptModelName:   "MODEL_B",
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
}
