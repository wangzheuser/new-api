package gemini

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGeminiResponsesHandlerReturnsOpenAIResponsesJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "gemini-responses-test")

	info := newGeminiResponsesRelayInfo(false)
	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "hello"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     2,
			CandidatesTokenCount: 3,
			TotalTokenCount:      5,
		},
	}
	body, err := common.Marshal(payload)
	require.NoError(t, err)

	usage, newAPIError := GeminiResponsesHandler(c, info, &http.Response{
		Body: io.NopCloser(bytes.NewReader(body)),
	})
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	assert.Equal(t, 2, usage.PromptTokens)
	assert.Equal(t, 3, usage.CompletionTokens)

	got := recorder.Body.String()
	assert.Contains(t, got, `"object":"response"`)
	assert.Contains(t, got, `"status":"completed"`)
	assert.Contains(t, got, `"type":"output_text"`)
	assert.Contains(t, got, `"text":"hello"`)
	assert.Contains(t, got, `"input_tokens":2`)
	assert.Contains(t, got, `"output_tokens":3`)
	assert.NotContains(t, got, `"choices"`)
	assert.NotContains(t, got, `"candidates"`)
}

// TestGeminiResponsesHandlerAppliesResponseOverrideToPromptBlock verifies a provider-level block is billed as success while the client response is replaced.
func TestGeminiResponsesHandlerAppliesResponseOverrideToPromptBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	info := newGeminiResponsesRelayInfo(false)
	info.ChannelMeta.ParamOverride = map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "response",
				"mode":  "return_error",
				"conditions": []interface{}{
					map[string]interface{}{
						"source": "semantic",
						"path":   "response.rejection_state",
						"mode":   "full",
						"value":  "all",
					},
				},
				"value": map[string]interface{}{
					"status_code": http.StatusForbidden,
					"code":        "response_rejected",
					"type":        "upstream_response_error",
					"message":     "模型拒绝执行该指令",
				},
			},
		},
	}
	relaycommon.StartResponseOverrideBuffer(c, info)

	blockReason := "SAFETY"
	payload := dto.GeminiChatResponse{
		PromptFeedback: &dto.GeminiChatPromptFeedback{BlockReason: &blockReason},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount: 7,
			TotalTokenCount:  7,
		},
	}
	body, err := common.Marshal(payload)
	require.NoError(t, err)

	usage, newAPIError := GeminiResponsesHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	})
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.PromptTokens)
	require.NotNil(t, info.ResponseOverride)
	assert.True(t, info.ResponseOverride.Applied)
	assert.Equal(t, relaycommon.ResponseRejectionAll, info.ResponseOverride.Semantics.Response.RejectionState)
	assert.Equal(t, relaycommon.ResponseUsageUpstream, info.ResponseOverride.Semantics.Response.UsageState)
	assert.Equal(t, http.StatusOK, info.ResponseOverride.UpstreamStatusCode)
	assert.Equal(t, http.StatusForbidden, info.ResponseOverride.ClientStatusCode)
	assert.True(t, info.ResponseOverride.Billable)
	assert.False(t, info.ResponseOverride.Retryable)
	assert.False(t, info.ResponseOverride.AffectsChannelHealth)

	buffer := relaycommon.CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	statusCode, _, candidateBody := buffer.Snapshot()
	assert.Equal(t, http.StatusBadRequest, statusCode)
	assert.Contains(t, string(candidateBody), "request blocked by Gemini API")
	assert.Empty(t, recorder.Body.String())
	buffer.Discard(c)
	assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(c))
	assert.Empty(t, recorder.Body.String())
}

// TestGeminiResponsesHandlerPreservesUnmatchedPromptBlock verifies a configured
// response rule that does not match leaves the legacy relay error intact.
func TestGeminiResponsesHandlerPreservesUnmatchedPromptBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	info := newGeminiResponsesRelayInfo(false)
	info.ChannelMeta.ParamOverride = map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "response",
				"mode":  "return_error",
				"conditions": []interface{}{
					map[string]interface{}{
						"source": "semantic",
						"path":   "response.rejection_state",
						"mode":   "full",
						"value":  "partial",
					},
				},
				"value": map[string]interface{}{"message": "not used"},
			},
		},
	}
	relaycommon.StartResponseOverrideBuffer(c, info)

	blockReason := "SAFETY"
	payload := dto.GeminiChatResponse{
		PromptFeedback: &dto.GeminiChatPromptFeedback{BlockReason: &blockReason},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount: 7,
			TotalTokenCount:  7,
		},
	}
	body, err := common.Marshal(payload)
	require.NoError(t, err)

	usage, newAPIError := GeminiResponsesHandler(c, info, &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	})
	require.NotNil(t, usage)
	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusBadRequest, newAPIError.StatusCode)
	require.NotNil(t, info.ResponseOverride)
	assert.True(t, info.ResponseOverride.Evaluated)
	assert.False(t, info.ResponseOverride.Applied)
	assert.Equal(t, relaycommon.ResponseOverrideNotAppliedNoMatch, info.ResponseOverride.NotAppliedReason)
	assert.Equal(t, relaycommon.ResponseRejectionAll, info.ResponseOverride.Semantics.Response.RejectionState)
	assert.Equal(t, relaycommon.ResponseUsageUpstream, info.ResponseOverride.Semantics.Response.UsageState)

	buffer := relaycommon.CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	buffer.MarkRelayError()
	buffer.Discard(c)
	assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(c))
	assert.Empty(t, recorder.Body.String())
}

func TestGeminiResponsesHandlerClosesBodyOnReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "gemini-responses-read-error-test")

	body := &failingReadCloser{}
	usage, newAPIError := GeminiResponsesHandler(c, newGeminiResponsesRelayInfo(false), &http.Response{Body: body})

	require.Nil(t, usage)
	require.NotNil(t, newAPIError)
	assert.True(t, body.closed)
}

func TestGeminiResponsesStreamHandlerReturnsOpenAIResponsesSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "gemini-responses-stream-test")

	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	info := newGeminiResponsesRelayInfo(true)
	first := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "hello"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     2,
			CandidatesTokenCount: 3,
			TotalTokenCount:      5,
		},
	}
	stop := "STOP"
	final := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				FinishReason: &stop,
				Content: dto.GeminiChatContent{
					Role:  "model",
					Parts: []dto.GeminiPart{{Text: ""}},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     2,
			CandidatesTokenCount: 3,
			TotalTokenCount:      5,
		},
	}
	firstData, err := common.Marshal(first)
	require.NoError(t, err)
	finalData, err := common.Marshal(final)
	require.NoError(t, err)
	streamBody := strings.Join([]string{
		"data: " + string(firstData),
		"",
		"data: " + string(finalData),
		"",
		"data: [DONE]",
		"",
	}, "\n")

	usage, newAPIError := GeminiResponsesStreamHandler(c, info, &http.Response{
		Body: io.NopCloser(strings.NewReader(streamBody)),
	})
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	assert.Contains(t, got, `event: response.created`)
	assert.Contains(t, got, `event: response.output_text.delta`)
	assert.Contains(t, got, `"delta":"hello"`)
	assert.Contains(t, got, `event: response.completed`)
	assert.Contains(t, got, `"input_tokens":2`)
	assert.Contains(t, got, `"output_tokens":3`)
	assert.NotContains(t, got, `"choices"`)
	assert.NotContains(t, got, `"candidates"`)
	requireOrderedGeminiResponsesSubstrings(t, got,
		`event: response.created`,
		`event: response.output_item.added`,
		`event: response.output_text.delta`,
		`event: response.output_text.done`,
		`event: response.completed`,
	)
}

func newGeminiResponsesRelayInfo(isStream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		IsStream:        isStream,
		RelayMode:       relayconstant.RelayModeResponses,
		RelayFormat:     types.RelayFormatOpenAIResponses,
		RequestURLPath:  "/v1/responses",
		DisablePing:     true,
		OriginModelName: "gemini-test",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-test",
		},
	}
}

type failingReadCloser struct {
	closed bool
}

func (r *failingReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (r *failingReadCloser) Close() error {
	r.closed = true
	return nil
}

func requireOrderedGeminiResponsesSubstrings(t *testing.T, s string, parts ...string) {
	t.Helper()
	offset := 0
	for _, part := range parts {
		idx := strings.Index(s[offset:], part)
		require.NotEqualf(t, -1, idx, "missing %q after byte offset %d", part, offset)
		offset += idx + len(part)
	}
}
