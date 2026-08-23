package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestSetEventStreamHeadersDisablesTransformation(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	SetEventStreamHeaders(c)

	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache, no-transform", recorder.Header().Get("Cache-Control"))
}

func TestObserveStreamDataPayloadCountsOnlyBusinessToolFragments(t *testing.T) {
	t.Parallel()
	chat := ObserveStreamDataPayload(
		`{"choices":[{"delta":{"tool_calls":[{"function":{"name":"天气","arguments":"{\"city\":"}}]}}]}`,
		types.RelayFormatOpenAI,
	)
	claude := ObserveStreamDataPayload(
		`{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"深圳\"}"}}`,
		types.RelayFormatClaude,
	)
	control := ObserveStreamDataPayload(
		`{"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		types.RelayFormatClaude,
	)

	assert.True(t, chat.Meaningful)
	assert.Equal(t, len([]byte("天气")), chat.ToolNameBytes)
	assert.Equal(t, len([]byte(`{"city":`)), chat.ToolArgumentBytes)
	assert.True(t, claude.Meaningful)
	assert.Equal(t, 0, claude.ToolNameBytes)
	assert.Equal(t, len([]byte(`深圳"}`)), claude.ToolArgumentBytes)
	assert.False(t, control.Meaningful)
	assert.Zero(t, control.ToolNameBytes)
	assert.Zero(t, control.ToolArgumentBytes)
}

func TestObserveStreamDataPayloadSupportsResponsesAndGemini(t *testing.T) {
	t.Parallel()
	responses := ObserveStreamDataPayload(
		`{"type":"response.function_call_arguments.delta","delta":"{\"city\":\"深圳"}`,
		types.RelayFormatOpenAIResponses,
	)
	gemini := ObserveStreamDataPayload(
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"weather","args":{"city":"深圳"}}}]}}]}`,
		types.RelayFormatGemini,
	)
	responsesControl := ObserveStreamDataPayload(
		`{"type":"response.created","response":{"id":"resp_1"}}`,
		types.RelayFormatOpenAIResponses,
	)

	assert.True(t, responses.Meaningful)
	assert.Equal(t, len([]byte(`{"city":"深圳`)), responses.ToolArgumentBytes)
	assert.True(t, gemini.Meaningful)
	assert.Equal(t, len([]byte("weather")), gemini.ToolNameBytes)
	assert.False(t, responsesControl.Meaningful)
}
