package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newResponsesChatTestContext(t *testing.T, body string, isStream bool) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "responses-test")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:           isStream,
		RelayFormat:        types.RelayFormatOpenAI,
		RelayMode:          relayconstant.RelayModeChatCompletions,
		ShouldIncludeUsage: true,
		DisablePing:        true,
	}
	return c, recorder, resp, info
}

func TestOaiResponsesToChatStreamHandlerConvertsSSEOrderAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-test","created_at":1710000000}}`,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)

	usage, err := OaiResponsesToChatStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `"role":"assistant"`)
	require.Contains(t, got, `"content":"hello"`)
	require.Contains(t, got, `"name":"lookup"`)
	require.Contains(t, got, `"arguments":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `"finish_reason":"tool_calls"`)
	require.Contains(t, got, `"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5`)
	require.Contains(t, got, `data: [DONE]`)
	requireOrderedSubstrings(t, got,
		`"role":"assistant"`,
		`"content":"hello"`,
		`"name":"lookup"`,
		`"arguments":"{\"q\":\"x\"}"`,
		`"finish_reason":"tool_calls"`,
		`"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5`,
		`data: [DONE]`,
	)
}

func TestOaiResponsesToChatBufferedStreamHandlerReturnsJSONFromSSE(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"buffered text"}`,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":\"x\"}"}`,
		`data: {"type":"response.done","response":{"model":"gpt-test","status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)

	usage, err := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 3, usage.TotalTokens)

	got := recorder.Body.String()
	require.NotContains(t, got, `data:`)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `"object":"chat.completion"`)
	require.Contains(t, got, `"content":"buffered text"`)
	require.Contains(t, got, `"name":"lookup"`)
	require.Contains(t, got, `"arguments":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `"finish_reason":"tool_calls"`)
}

// TestOaiResponsesToChatBufferedStreamHandlerAppliesResponseOverride verifies
// an SSE-to-JSON conversion remains eligible for non-streaming response rules.
func TestOaiResponsesToChatBufferedStreamHandlerAppliesResponseOverride(t *testing.T) {
	body := `data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[],"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2}}}` + "\n\n"
	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)
	resp.Header.Set("Content-Encoding", "gzip")
	resp.Header.Set("ETag", `"upstream-sse"`)
	info.ChannelMeta.ParamOverride = map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"id":    "responses_rejection",
				"phase": "response",
				"mode":  "return_error",
				"conditions": []interface{}{
					map[string]interface{}{
						"source": "semantic",
						"path":   "response.primary_outcome",
						"mode":   "full",
						"value":  relaycommon.ResponseOutcomeRejected,
					},
				},
				"value": map[string]interface{}{
					"message":     "response rejected",
					"status_code": http.StatusInternalServerError,
				},
			},
		},
	}
	relaycommon.StartResponseOverrideBuffer(c, info)

	usage, apiError := OaiResponsesToChatBufferedStreamHandler(c, info, resp)
	decision := service.EvaluateResponseOverrideBeforeSettlement(c, info, usage, http.StatusOK)

	require.Nil(t, apiError)
	require.NotNil(t, usage)
	require.NotNil(t, info.ResponseOverride)
	require.Same(t, info.ResponseOverride, decision)
	require.True(t, info.ResponseOverride.Evaluated)
	require.True(t, info.ResponseOverride.Applied)
	require.Equal(t, "responses_rejection", info.ResponseOverride.RuleID)
	require.Equal(t, relaycommon.ResponseOutcomeRejected, info.ResponseOverride.Semantics.Response.PrimaryOutcome)
	require.Empty(t, recorder.Body.String())
	statusCode, headers, candidateBody := relaycommon.CurrentResponseOverrideBuffer(c).Snapshot()
	require.Equal(t, http.StatusOK, statusCode)
	require.Equal(t, "application/json", headers.Get("Content-Type"))
	require.Empty(t, headers.Get("Content-Encoding"))
	require.Empty(t, headers.Get("ETag"))
	require.Contains(t, string(candidateBody), `"finish_reason":"content_filter"`)
}

// TestOaiResponsesToChatBufferedStreamHandlerPreservesProviderTerminalSemantics
// verifies conversion cannot erase terminal facts collected from Responses SSE.
func TestOaiResponsesToChatBufferedStreamHandlerPreservesProviderTerminalSemantics(t *testing.T) {
	tests := []struct {
		name             string
		terminal         string
		outcome          string
		rejection        string
		providerReason   string
		normalizedReason string
		truncated        bool
		finishReason     string
	}{
		{
			name:             "incomplete max output tokens",
			terminal:         `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
			outcome:          relaycommon.ResponseOutcomeIncomplete,
			rejection:        relaycommon.ResponseRejectionNone,
			providerReason:   "max_output_tokens",
			normalizedReason: "max_tokens",
			truncated:        true,
			finishReason:     "length",
		},
		{
			name:             "incomplete content filter",
			terminal:         `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[],"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2}}}`,
			outcome:          relaycommon.ResponseOutcomeRejected,
			rejection:        relaycommon.ResponseRejectionAll,
			providerReason:   "content_filter",
			normalizedReason: "content_filter",
			finishReason:     "content_filter",
		},
		{
			name:             "completed refusal output",
			terminal:         `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","text":"policy refusal"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
			outcome:          relaycommon.ResponseOutcomeRejected,
			rejection:        relaycommon.ResponseRejectionAll,
			providerReason:   "completed",
			normalizedReason: "content_filter",
			finishReason:     "stop",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := "data: " + test.terminal + "\n\ndata: [DONE]\n\n"
			c, recorder, resp, info := newResponsesChatTestContext(t, body, false)

			usage, apiError := OaiResponsesToChatBufferedStreamHandler(c, info, resp)

			require.Nil(t, apiError)
			require.NotNil(t, usage)
			require.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.ResponseSemantics.Upstream.Format)
			require.Equal(t, test.outcome, info.ResponseSemantics.Response.PrimaryOutcome)
			require.Equal(t, test.rejection, info.ResponseSemantics.Response.RejectionState)
			require.Equal(t, test.providerReason, info.ResponseSemantics.Response.ProviderReason)
			require.Equal(t, test.normalizedReason, info.ResponseSemantics.Response.NormalizedReason)
			require.Equal(t, test.truncated, info.ResponseSemantics.Response.Truncated)
			require.Contains(t, recorder.Body.String(), `"finish_reason":"`+test.finishReason+`"`)
		})
	}
}

// TestOaiResponsesToChatBufferedStreamHandlerPreservesFailedSemantics verifies
// the relay error path still records the provider-native failed terminal state.
func TestOaiResponsesToChatBufferedStreamHandlerPreservesFailedSemantics(t *testing.T) {
	body := `data: {"type":"response.failed","error":{"type":"server_error","code":"upstream_aborted","message":"mid stream aborted"}}` + "\n\n"
	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)

	usage, apiError := OaiResponsesToChatBufferedStreamHandler(c, info, resp)

	require.Nil(t, usage)
	require.NotNil(t, apiError)
	require.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.ResponseSemantics.Upstream.Format)
	require.Equal(t, relaycommon.ResponseOutcomeFailed, info.ResponseSemantics.Response.PrimaryOutcome)
	require.Equal(t, relaycommon.ResponseRejectionNone, info.ResponseSemantics.Response.RejectionState)
	require.Equal(t, "failed", info.ResponseSemantics.Response.ProviderReason)
	require.Empty(t, recorder.Body.String())
}

func TestOaiChatToResponsesStreamHandlerConvertsSSEOrderAndUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":\"x\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	usage, err := OaiChatToResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 2, usage.PromptTokens)
	require.Equal(t, 3, usage.CompletionTokens)
	require.Equal(t, 5, usage.TotalTokens)

	got := recorder.Body.String()
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	require.Contains(t, got, `event: response.created`)
	require.Contains(t, got, `event: response.output_text.delta`)
	require.Contains(t, got, `"delta":"hello"`)
	require.Contains(t, got, `event: response.function_call_arguments.delta`)
	require.Contains(t, got, `"delta":"{\"q\":\"x\"}"`)
	require.Contains(t, got, `event: response.completed`)
	require.Contains(t, got, `"input_tokens":2`)
	require.Contains(t, got, `"output_tokens":3`)
	requireOrderedSubstrings(t, got,
		`event: response.created`,
		`event: response.output_item.added`,
		`event: response.output_text.delta`,
		`event: response.output_item.added`,
		`event: response.function_call_arguments.delta`,
		`event: response.output_text.done`,
		`event: response.function_call_arguments.done`,
		`event: response.completed`,
	)
}

func TestOaiChatToResponsesStreamHandlerRejectsMissingDoneMarker(t *testing.T) {
	body := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}` + "\n"
	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	usage, apiError := OaiChatToResponsesStreamHandler(c, info, resp)

	require.Nil(t, usage)
	require.NotNil(t, apiError)
	require.True(t, types.IsSkipRetryError(apiError))
	require.Contains(t, recorder.Body.String(), "partial")
	require.NotContains(t, recorder.Body.String(), "event: response.completed")
}

func TestOaiResponsesToChatStreamHandlerRejectsMissingTypedTerminal(t *testing.T) {
	body := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n"
	c, recorder, resp, info := newResponsesChatTestContext(t, body, true)

	usage, apiError := OaiResponsesToChatStreamHandler(c, info, resp)

	require.Nil(t, usage)
	require.NotNil(t, apiError)
	require.True(t, types.IsSkipRetryError(apiError))
	require.Contains(t, recorder.Body.String(), "partial")
}

func TestOaiResponsesToChatBufferedStreamHandlerRejectsMissingTypedTerminal(t *testing.T) {
	body := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n"
	c, recorder, resp, info := newResponsesChatTestContext(t, body, false)

	usage, apiError := OaiResponsesToChatBufferedStreamHandler(c, info, resp)

	require.Nil(t, usage)
	require.NotNil(t, apiError)
	require.Equal(t, types.ErrorCodeBadResponse, apiError.GetErrorCode())
	require.Empty(t, recorder.Body.String())
}

func requireOrderedSubstrings(t *testing.T, s string, parts ...string) {
	t.Helper()

	offset := 0
	for _, part := range parts {
		idx := strings.Index(s[offset:], part)
		require.NotEqualf(t, -1, idx, "missing %q after byte offset %d", part, offset)
		offset += idx + len(part)
	}
}
