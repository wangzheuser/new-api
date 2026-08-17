package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newOpenAIStreamTestContext builds the minimum relay state needed by the
// legacy OpenAI-compatible stream handler.
func newOpenAIStreamTestContext(body string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *http.Response) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		IsStream:           true,
		DisablePing:        true,
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        types.RelayFormatOpenAI,
		StartTime:          time.Now(),
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_X"},
		ShouldIncludeUsage: true,
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	return c, recorder, info, resp
}

func TestOaiStreamHandlerRejectsEOFMissingTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`,
		``,
	}, "\n")
	c, recorder, info, resp := newOpenAIStreamTestContext(body)

	usage, apiError := OaiStreamHandler(c, info, resp)

	require.NotNil(t, usage)
	assert.Greater(t, usage.CompletionTokens, 0)
	require.NotNil(t, apiError)
	assert.True(t, types.IsSkipRetryError(apiError))
	assert.Contains(t, recorder.Body.String(), "first")
	assert.NotContains(t, recorder.Body.String(), "data: [DONE]")
	reason, endErr := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonUnexpectedEOF, reason)
	assert.EqualError(t, endErr, "stream ended before terminal event")
}

func TestOaiStreamHandlerAcceptsFinishReasonWithoutDoneMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"content":"complete"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
		``,
	}, "\n")
	c, recorder, info, resp := newOpenAIStreamTestContext(body)

	usage, apiError := OaiStreamHandler(c, info, resp)

	require.Nil(t, apiError)
	require.NotNil(t, usage)
	assert.Equal(t, 3, usage.TotalTokens)
	assert.Contains(t, recorder.Body.String(), `"finish_reason":"stop"`)
	assert.Contains(t, recorder.Body.String(), "data: [DONE]")
	reason, endErr := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonDone, reason)
	assert.NoError(t, endErr)
	event, status := info.StreamStatus.Terminal()
	assert.Equal(t, "chat.finish_reason", event)
	assert.Equal(t, "completed", status)
}

func TestOaiStreamHandlerProgressiveRequiresDoneMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"content":"complete"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
	}, "\n")
	c, recorder, info, resp := newOpenAIStreamTestContext(body)
	resp.Header = http.Header{"X-Stream-Policy": []string{"progressive-v1"}}

	usage, apiError := OaiStreamHandler(c, info, resp)

	require.NotNil(t, usage)
	require.NotNil(t, apiError)
	assert.Contains(t, apiError.Error(), "stream ended before terminal event [DONE]")
	assert.NotContains(t, recorder.Body.String(), `"finish_reason":"stop"`)
	assert.NotContains(t, recorder.Body.String(), "data: [DONE]")
	reason, _ := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonUnexpectedEOF, reason)
}

func TestOaiStreamHandlerProgressiveFlushesTrustedToolNameBeforeNextEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather"}}]},"finish_reason":null}]}`,
		``,
	}, "\n")
	c, recorder, info, resp := newOpenAIStreamTestContext(body)
	resp.Header = http.Header{"X-Stream-Policy": []string{"progressive-v1"}}

	usage, apiError := OaiStreamHandler(c, info, resp)

	require.NotNil(t, usage)
	require.NotNil(t, apiError)
	assert.Contains(t, recorder.Body.String(), `"name":"get_weather"`)
	assert.True(t, info.StreamStatus.ClientPayloadIsCommitted())
	assert.NotContains(t, recorder.Body.String(), "data: [DONE]")
}

func TestHandleClaudeFormatReturnsClientCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(requestContext)
	cancel()
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		},
	}

	err := handleClaudeFormat(c, `{"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`, info)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestOaiStreamHandlerPreservesUpstreamErrorEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`,
		`data: {"error":{"type":"upstream_timeout","code":"deadline_exceeded","message":"upstream generation expired"}}`,
		``,
	}, "\n")
	c, recorder, info, resp := newOpenAIStreamTestContext(body)
	resp.Header = http.Header{"X-Stream-Policy": []string{"progressive-v1"}}

	usage, apiError := OaiStreamHandler(c, info, resp)

	require.NotNil(t, usage)
	require.NotNil(t, apiError)
	openAIError := apiError.ToOpenAIError()
	assert.Equal(t, "upstream_timeout", openAIError.Type)
	assert.Equal(t, "deadline_exceeded", openAIError.Code)
	assert.Equal(t, "upstream generation expired", openAIError.Message)
	assert.Contains(t, recorder.Body.String(), "partial")
	assert.NotContains(t, recorder.Body.String(), "deadline_exceeded")
	event, status := info.StreamStatus.Terminal()
	assert.Equal(t, "error", event)
	assert.Equal(t, "failed", status)
}
