package controller

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClientContextUsageWriterFrames verifies the real final writer preserves fragmented SSE and sparse Claude updates.
func TestClientContextUsageWriterFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	c.Header("Content-Type", "text/event-stream")
	info := &relaycommon.RelayInfo{RequestedModelName: "client-model", RelayFormat: types.RelayFormatClaude, ContextTruncation: &relaycommon.ContextTruncationState{Applied: true, Before: 1000}}
	installRequestedModelResponseWriter(c, info)
	relaycommon.ApplyFinalResponseWriter(c)
	_, err := c.Writer.Write([]byte("event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"upstream-model\",\"usage\":{\"input_tokens\":"))
	require.NoError(t, err)
	c.Writer.Flush()
	assert.Equal(t, "event: message_start\r\n", r.Body.String())
	_, err = c.Writer.Write([]byte("50,\"output_tokens\":0,\"cache_read_input_tokens\":100}}}\r\n\r\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":20}}\n\ndata: {\"type\":\"message_stop\"}"))
	require.NoError(t, err)
	c.Writer.Flush()
	require.NoError(t, relaycommon.FinishFinalResponseWriter(c, true))
	assert.Equal(t, "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"client-model\",\"usage\":{\"input_tokens\":900,\"output_tokens\":0,\"cache_read_input_tokens\":100}}}\r\n\r\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":20}}\n\ndata: {\"type\":\"message_stop\"}", r.Body.String())
	assert.EqualValues(t, 1000, info.ContextTruncation.Reported)
}

// TestClientContextUsageWriterRetryReset ensures stale counters and incomplete bytes never leak into a new attempt.
func TestClientContextUsageWriterRetryReset(t *testing.T) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ContextTruncation: &relaycommon.ContextTruncationState{Applied: true, Before: 1000}}
	installRequestedModelResponseWriter(c, info)
	relaycommon.ApplyFinalResponseWriter(c)
	c.Header("Content-Type", "application/json")
	_, err := c.Writer.Write([]byte(`{"usage":{"input_tokens":`))
	require.NoError(t, err)
	require.NoError(t, relaycommon.FinishFinalResponseWriter(c, false))
	assert.Empty(t, r.Body.String())
	// InitInputPolicyState replaces attempt-local state in production; a compacted request is not truncated.
	info.ContextTruncation = &relaycommon.ContextTruncationState{Before: 100}
	relaycommon.ApplyFinalResponseWriter(c)
	c.Header("Content-Type", "application/json")
	_, err = c.Writer.Write([]byte(`{"usage":{"input_tokens":90,"output_tokens":1}}`))
	require.NoError(t, err)
	require.NoError(t, relaycommon.FinishFinalResponseWriter(c, true))
	assert.JSONEq(t, `{"usage":{"input_tokens":90,"output_tokens":1}}`, r.Body.String())
}

// TestClientContextUsageWriterDoesNotAddUsage preserves clients that opted out and final error envelopes.
func TestClientContextUsageWriterDoesNotAddUsage(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
	}{
		{200, `{"choices":[],"usage":null}`},
		{401, `{"error":{"message":"denied"},"usage":{"prompt_tokens":10}}`},
	} {
		r := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(r)
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ContextTruncation: &relaycommon.ContextTruncationState{Applied: true, Before: 1000}}
		installRequestedModelResponseWriter(c, info)
		relaycommon.ApplyFinalResponseWriter(c)
		c.Header("Content-Type", "application/json")
		c.Writer.WriteHeader(tt.status)
		_, err := c.Writer.Write([]byte(tt.body))
		require.NoError(t, err)
		require.NoError(t, relaycommon.FinishFinalResponseWriter(c, true))
		assert.Equal(t, tt.body, r.Body.String())
		assert.Equal(t, tt.status, r.Code)
		assert.Zero(t, info.ContextTruncation.Reported)
	}
}
