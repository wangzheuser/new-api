package common

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResponseOverrideBufferCommit verifies a candidate is invisible until it
// is explicitly committed with its status, headers, and body intact.
func TestResponseOverrideBufferCommit(t *testing.T) {
	c, recorder, info := newResponseOverrideBufferTestContext(t, responseRule(
		map[string]interface{}{"message": "blocked"},
		map[string]interface{}{"source": "body", "path": "blocked", "mode": "full", "value": true},
	))
	buffer := CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)

	c.Header("X-Upstream", "kept")
	c.Status(http.StatusAccepted)
	_, err := c.Writer.Write([]byte(`{"ok":true}`))
	require.NoError(t, err)
	assert.False(t, recorder.Flushed)
	assert.Equal(t, 0, recorder.Body.Len())
	assert.Empty(t, recorder.Header().Get("X-Upstream"))

	decision := buffer.Evaluate(http.StatusOK)
	require.NotNil(t, decision)
	assert.False(t, decision.Applied)
	assert.Equal(t, ResponseOverrideNotAppliedNoMatch, decision.NotAppliedReason)
	require.NoError(t, buffer.Commit(c))

	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.False(t, recorder.Flushed)
	assert.Equal(t, "kept", recorder.Header().Get("X-Upstream"))
	assert.JSONEq(t, `{"ok":true}`, recorder.Body.String())
	assert.Same(t, buffer.ResponseWriter, c.Writer)
	assert.Equal(t, info.ResponseSemantics, decision.Semantics)
}

// TestResponseOverrideBufferMatch verifies a semantic rejection is retained as
// an independent client decision while the candidate response stays uncommitted.
func TestResponseOverrideBufferMatch(t *testing.T) {
	c, recorder, info := newResponseOverrideBufferTestContext(t, responseRule(
		map[string]interface{}{
			"message":     "模型拒绝执行该指令",
			"status_code": http.StatusInternalServerError,
			"code":        "model_refused",
		},
		map[string]interface{}{"source": "semantic", "path": "response.primary_outcome", "mode": "full", "value": ResponseOutcomeRejected},
	))
	buffer := CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	buffer.ResponseWriter.Header().Set("Access-Control-Allow-Origin", "https://client.example")
	buffer.ResponseWriter.Header().Set("X-Oneapi-Request-Id", "request-id")
	buffer.ResponseWriter.Header().Set("Last-Modified", "yesterday")
	body := []byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}],"usage":{"total_tokens":12}}`)
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("Content-Length", "999")
	c.Writer.Header().Set("Content-Encoding", "gzip")
	c.Writer.Header().Set("ETag", "candidate-tag")
	c.Writer.WriteHeader(http.StatusOK)
	_, err := c.Writer.Write(body)
	require.NoError(t, err)

	decision := EvaluateResponseOverride(c, http.StatusOK)
	require.NotNil(t, decision)
	require.True(t, decision.Applied)
	require.NotNil(t, decision.ClientError)
	assert.Equal(t, http.StatusOK, decision.UpstreamStatusCode)
	assert.Equal(t, http.StatusOK, decision.CandidateStatusCode)
	assert.Equal(t, http.StatusInternalServerError, decision.ClientStatusCode)
	assert.Equal(t, 1, decision.RuleIndex)
	assert.Equal(t, http.StatusInternalServerError, decision.Semantics.Client.HTTPStatus)
	assert.True(t, decision.Billable)
	assert.False(t, decision.Retryable)
	assert.False(t, decision.AffectsChannelHealth)
	assert.Equal(t, ResponseOutcomeRejected, decision.Semantics.Response.PrimaryOutcome)
	assert.Equal(t, http.StatusInternalServerError, info.ResponseSemantics.Client.HTTPStatus)
	assert.Equal(t, decision, info.ResponseOverride)
	assert.Equal(t, 0, recorder.Body.Len())

	buffer.Discard(c)
	assert.Equal(t, 0, recorder.Body.Len())
	assert.Empty(t, recorder.Header().Get("Content-Length"))
	assert.Empty(t, recorder.Header().Get("Content-Encoding"))
	assert.Empty(t, recorder.Header().Get("Content-Type"))
	assert.Empty(t, recorder.Header().Get("ETag"))
	assert.Empty(t, recorder.Header().Get("Last-Modified"))
	assert.Equal(t, "https://client.example", recorder.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "request-id", recorder.Header().Get("X-Oneapi-Request-Id"))
	assert.Nil(t, CurrentResponseOverrideBuffer(c))
}

// TestResponseOverrideBufferFailOpen verifies invalid runtime rules preserve
// the successful candidate while recording a configuration error.
func TestResponseOverrideBufferFailOpen(t *testing.T) {
	c, recorder, _ := newResponseOverrideBufferTestContext(t, responseRule(
		map[string]interface{}{"message": "bad", "skip_retry": false},
	))
	buffer := CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	c.Writer.WriteHeader(http.StatusOK)
	_, err := c.Writer.Write([]byte(`{"choices":[]}`))
	require.NoError(t, err)

	decision := buffer.Evaluate(http.StatusOK)
	require.NotNil(t, decision)
	assert.False(t, decision.Applied)
	assert.Equal(t, ResponseOverrideNotAppliedConfigError, decision.NotAppliedReason)
	assert.NotEmpty(t, decision.ConfigError)
	require.NoError(t, buffer.Commit(c))
	assert.JSONEq(t, `{"choices":[]}`, recorder.Body.String())
}

// TestResponseOverrideBufferMalformedConditionsFailOpen verifies raw response
// detection still installs a buffer when a persisted condition is malformed.
func TestResponseOverrideBufferMalformedConditionsFailOpen(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase":      "response",
				"mode":       "return_error",
				"value":      map[string]interface{}{"message": "blocked"},
				"conditions": "invalid",
			},
		},
	}
	c, recorder, _ := newResponseOverrideBufferTestContext(t, override)
	buffer := CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	c.Writer.WriteHeader(http.StatusOK)
	_, err := c.Writer.Write([]byte(`{"choices":[]}`))
	require.NoError(t, err)

	decision := buffer.Evaluate(http.StatusOK)
	require.NotNil(t, decision)
	assert.False(t, decision.Applied)
	assert.Equal(t, ResponseOverrideNotAppliedConfigError, decision.NotAppliedReason)
	assert.Contains(t, decision.ConfigError, "operations format is invalid")
	require.NoError(t, buffer.Commit(c))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"choices":[]}`, recorder.Body.String())
}

// TestResponseOverrideBufferRetryIsolation verifies one discarded attempt does
// not leak body or headers into the next selected channel.
func TestResponseOverrideBufferRetryIsolation(t *testing.T) {
	c, recorder, firstInfo := newResponseOverrideBufferTestContext(t, responseRule(map[string]interface{}{"message": "blocked"}))
	first := CurrentResponseOverrideBuffer(c)
	require.NotNil(t, first)
	firstInfo.ResponseSemantics = ClassifyResponseSemantics(types.RelayFormatOpenAI, []byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`))
	c.Writer.Header().Set("X-Attempt", "first")
	_, err := c.Writer.Write([]byte("first-body"))
	require.NoError(t, err)
	first.MarkRelayError()
	first.Discard(c)
	assert.Equal(t, ResponseOverrideNotAppliedRelayError, firstInfo.ResponseOverride.NotAppliedReason)

	secondInfo := &RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &ChannelMeta{ParamOverride: responseRule(
			map[string]interface{}{"message": "blocked"},
			map[string]interface{}{"source": "body", "path": "blocked", "mode": "full", "value": true},
		)},
	}
	StartResponseOverrideBuffer(c, secondInfo)
	second := CurrentResponseOverrideBuffer(c)
	require.NotNil(t, second)
	assert.Equal(t, ResponseSemantics{}, secondInfo.ResponseSemantics)
	c.Writer.Header().Set("X-Attempt", "second")
	_, err = c.Writer.Write([]byte(`{"ok":true}`))
	require.NoError(t, err)
	second.Evaluate(http.StatusOK)
	require.NoError(t, second.Commit(c))

	assert.Equal(t, "second", recorder.Header().Get("X-Attempt"))
	assert.JSONEq(t, `{"ok":true}`, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "first-body")
}

// TestResponseOverrideBufferStreaming records a configured rule as skipped and
// leaves streaming writes transparent.
func TestResponseOverrideBufferStreaming(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		IsStream:    true,
		ChannelMeta: &ChannelMeta{ParamOverride: responseRule(map[string]interface{}{"message": "blocked"})},
	}
	StartResponseOverrideBuffer(c, info)

	assert.Nil(t, CurrentResponseOverrideBuffer(c))
	require.NotNil(t, info.ResponseOverride)
	assert.Equal(t, ResponseOverrideNotAppliedStreaming, info.ResponseOverride.NotAppliedReason)
	_, err := c.Writer.Write([]byte("data: ok\n\n"))
	require.NoError(t, err)
	assert.Equal(t, "data: ok\n\n", recorder.Body.String())
}

// TestResponseOverrideBufferGeminiNativeStreaming verifies native Gemini SSE records the configured streaming bypass.
func TestResponseOverrideBufferGeminiNativeStreaming(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &RelayInfo{
		RelayFormat:    types.RelayFormatGemini,
		RequestURLPath: "/v1beta/models/gemini:streamGenerateContent?alt=sse",
		IsStream:       true,
		ChannelMeta:    &ChannelMeta{ParamOverride: responseRule(map[string]interface{}{"message": "blocked"})},
	}

	StartResponseOverrideBuffer(c, info)

	assert.Nil(t, CurrentResponseOverrideBuffer(c))
	require.NotNil(t, info.ResponseOverride)
	assert.True(t, info.ResponseOverride.Configured)
	assert.Equal(t, ResponseOverrideNotAppliedStreaming, info.ResponseOverride.NotAppliedReason)
}

// TestResponseOverrideBufferClaudeMessages verifies the native Messages route
// installs a response slot even though its relay mode remains unknown.
func TestResponseOverrideBufferClaudeMessages(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &RelayInfo{
		RelayFormat:    types.RelayFormatClaude,
		RelayMode:      relayconstant.RelayModeUnknown,
		RequestURLPath: "/v1/messages",
		ChannelMeta:    &ChannelMeta{ParamOverride: responseRule(map[string]interface{}{"message": "blocked"})},
	}

	StartResponseOverrideBuffer(c, info)

	assert.NotNil(t, CurrentResponseOverrideBuffer(c))
	require.NotNil(t, info.ResponseOverride)
	assert.True(t, info.ResponseOverride.Configured)
}

// TestResponseOverrideBufferUnexpectedStreaming releases a response whose
// upstream content type reveals streaming after the initial request decision.
func TestResponseOverrideBufferUnexpectedStreaming(t *testing.T) {
	c, recorder, info := newResponseOverrideBufferTestContext(t, responseRule(map[string]interface{}{"message": "blocked"}))
	require.NotNil(t, CurrentResponseOverrideBuffer(c))
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	_, err := c.Writer.Write([]byte("data: first\n\n"))
	require.NoError(t, err)

	assert.Nil(t, CurrentResponseOverrideBuffer(c))
	assert.Equal(t, ResponseOverrideNotAppliedStreaming, info.ResponseOverride.NotAppliedReason)
	assert.True(t, info.ResponseOverride.Configured)
	assert.False(t, info.ResponseOverride.Applied)
	assert.Equal(t, "data: first\n\n", recorder.Body.String())
}

// TestResponseOverrideBufferLimitFailsOpen verifies oversized responses are
// released transparently instead of being evaluated or retained in memory.
func TestResponseOverrideBufferLimitFailsOpen(t *testing.T) {
	c, recorder, info := newResponseOverrideBufferTestContext(t, responseRule(map[string]interface{}{"message": "blocked"}))
	buffer := CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	prefix := bytes.Repeat([]byte("a"), maxResponseOverrideBufferBytes)
	suffix := []byte("tail")

	_, err := c.Writer.Write(prefix)
	require.NoError(t, err)
	assert.Equal(t, 0, recorder.Body.Len())
	_, err = c.Writer.Write(suffix)
	require.NoError(t, err)

	assert.Nil(t, CurrentResponseOverrideBuffer(c))
	require.NotNil(t, info.ResponseOverride)
	assert.False(t, info.ResponseOverride.Evaluated)
	assert.False(t, info.ResponseOverride.Applied)
	assert.Equal(t, ResponseOverrideNotAppliedBufferLimit, info.ResponseOverride.NotAppliedReason)
	assert.Equal(t, maxResponseOverrideBufferBytes+len(suffix), recorder.Body.Len())
	assert.Equal(t, suffix, recorder.Body.Bytes()[maxResponseOverrideBufferBytes:])
}

// TestResponseOverrideBufferRestoresFinalWriter verifies release rebuilds the
// final client transform without retaining the attempt-scoped response buffer.
func TestResponseOverrideBufferRestoresFinalWriter(t *testing.T) {
	c, _, _ := newResponseOverrideBufferTestContext(t, responseRule(
		map[string]interface{}{"message": "blocked"},
		map[string]interface{}{"source": "body", "path": "blocked", "mode": "full", "value": true},
	))
	buffer := CurrentResponseOverrideBuffer(c)
	require.NotNil(t, buffer)
	SetFinalResponseWriterFactory(c, func(writer gin.ResponseWriter) FinalResponseWriter {
		return &finalResponseWriterForTest{ResponseWriter: writer}
	})
	ApplyFinalResponseWriter(c)

	_, err := c.Writer.Write([]byte(`{"ok":true}`))
	require.NoError(t, err)
	buffer.Evaluate(http.StatusOK)
	require.NoError(t, buffer.Commit(c))

	restored, ok := c.Writer.(*finalResponseWriterForTest)
	require.True(t, ok)
	assert.Same(t, buffer.ResponseWriter, restored.ResponseWriter)
	assert.NotSame(t, buffer, restored.ResponseWriter)
	assert.Nil(t, CurrentResponseOverrideBuffer(c))
}

type finalResponseWriterForTest struct {
	gin.ResponseWriter
}

// RebindResponseWriter replaces the attempt buffer used by the test transform.
func (w *finalResponseWriterForTest) RebindResponseWriter(writer gin.ResponseWriter) {
	w.ResponseWriter = writer
}

// FinishResponseWriter completes the no-op test transform.
func (w *finalResponseWriterForTest) FinishResponseWriter(bool) error {
	return nil
}

func newResponseOverrideBufferTestContext(t *testing.T, override map[string]interface{}) (*gin.Context, *httptest.ResponseRecorder, *RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &ChannelMeta{ParamOverride: override},
	}
	StartResponseOverrideBuffer(c, info)
	return c, recorder, info
}
