package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClaudeStreamFixture(body string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, *http.Response) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		DisablePing: true,
		RelayFormat: types.RelayFormatClaude,
		StartTime:   time.Now(),
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_X"},
	}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Stream-Policy": []string{"progressive-v1"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	return ctx, recorder, info, response
}

func TestClaudeStreamHandlerPartialToolFailureUsesFlushedToolUsage(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })
	body := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"深圳\"}"}}`,
		`data: {"type":"error","error":{"type":"api_error","message":"upstream generation stopped"}}`,
		``,
	}, "\n")
	ctx, recorder, info, response := newClaudeStreamFixture(body)

	usage, relayErr := ClaudeStreamHandler(ctx, response, info)

	require.NotNil(t, relayErr)
	require.NotNil(t, usage)
	assert.Greater(t, usage.CompletionTokens, 0)
	assert.True(t, info.StreamStatus.ClientPayloadIsCommitted())
	nameBytes, argumentBytes := info.StreamStatus.ToolPayloadBytes()
	assert.Equal(t, int64(len([]byte("get_weather"))), nameBytes)
	assert.Equal(t, int64(len([]byte(`{"city":"深圳"}`))), argumentBytes)
	assert.Contains(t, recorder.Body.String(), "get_weather")
	assert.NotContains(t, recorder.Body.String(), "message_stop")
}

func TestClaudeStreamHandlerSuccessWritesOneMessageStop(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })
	body := strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","model":"MODEL_X","usage":{"input_tokens":2,"output_tokens":0}}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	ctx, recorder, info, response := newClaudeStreamFixture(body)

	usage, relayErr := ClaudeStreamHandler(ctx, response, info)

	require.Nil(t, relayErr)
	require.NotNil(t, usage)
	assert.Equal(t, 3, usage.TotalTokens)
	assert.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"message_stop"`))
	reason, endErr := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonDone, reason)
	assert.NoError(t, endErr)
}

func TestClaudeStreamHandlerConvertedOutputTracksCommittedPayloadWithoutPolicyHeader(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })
	body := strings.Join([]string{
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		`data: {"type":"error","error":{"type":"api_error","message":"upstream generation stopped"}}`,
		``,
	}, "\n")
	ctx, recorder, info, response := newClaudeStreamFixture(body)
	info.RelayFormat = types.RelayFormatOpenAI
	response.Header = http.Header{}

	usage, relayErr := ClaudeStreamHandler(ctx, response, info)

	require.NotNil(t, relayErr)
	require.NotNil(t, usage)
	assert.True(t, info.StreamStatus.ClientPayloadIsCommitted())
	assert.Contains(t, recorder.Body.String(), "partial")
	assert.Empty(t, info.StreamStatus.StreamPolicyVersion())
}
