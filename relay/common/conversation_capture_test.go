package common

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type shortConversationResponseWriter struct {
	gin.ResponseWriter
	limit int
}

// Write simulates a response writer that only accepts a prefix.
func (writer *shortConversationResponseWriter) Write(data []byte) (int, error) {
	_, _ = writer.ResponseWriter.Write(data[:writer.limit])
	return writer.limit, io.ErrShortWrite
}

// WriteString simulates a string response writer that only accepts a prefix.
func (writer *shortConversationResponseWriter) WriteString(data string) (int, error) {
	_, _ = writer.ResponseWriter.WriteString(data[:writer.limit])
	return writer.limit, io.ErrShortWrite
}

// TestConversationCaptureBounds verifies large bodies retain totals without unbounded memory use.
func TestConversationCaptureBounds(t *testing.T) {
	capture := NewConversationCapture()
	body := bytes.Repeat([]byte("x"), conversationCaptureSegmentLimit+257)
	capture.append(&capture.clientResponse, body)

	snapshot := capture.Snapshot()
	assert.Len(t, snapshot.ClientResponseBody, conversationCaptureSegmentLimit)
	assert.EqualValues(t, len(body), snapshot.ClientResponseBytes)
	assert.True(t, snapshot.ClientResponseTruncated)
}

// TestConversationCaptureEligibility excludes binary and non-conversation relay modes.
func TestConversationCaptureEligibility(t *testing.T) {
	tests := []struct {
		name     string
		info     RelayInfo
		expected bool
	}{
		{name: "chat", info: RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions}, expected: true},
		{name: "image", info: RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeImagesGenerations}, expected: false},
		{name: "claude", info: RelayInfo{RelayFormat: types.RelayFormatClaude}, expected: true},
		{name: "gemini generation", info: RelayInfo{RelayFormat: types.RelayFormatGemini, RequestURLPath: "/v1beta/models/gemini:generateContent"}, expected: true},
		{name: "gemini embedding", info: RelayInfo{RelayFormat: types.RelayFormatGemini, RequestURLPath: "/v1beta/models/text:embedContent"}, expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, isConversationCaptureEligible(&test.info))
		})
	}
}

// TestConversationCaptureUpstreamWrappers verifies request and response bodies remain readable.
func TestConversationCaptureUpstreamWrappers(t *testing.T) {
	capture := NewConversationCapture()
	info := &RelayInfo{ConversationCapture: capture}

	request, err := http.NewRequest(http.MethodPost, "https://example.com", bytes.NewBufferString("request-body"))
	require.NoError(t, err)
	WrapConversationUpstreamRequest(info, request)
	requestBody, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	assert.Equal(t, "request-body", string(requestBody))

	response := &http.Response{Body: io.NopCloser(bytes.NewBufferString("response-body"))}
	WrapConversationUpstreamResponse(info, response)
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "response-body", string(responseBody))

	snapshot := capture.Snapshot()
	assert.Equal(t, "request-body", string(snapshot.UpstreamRequestBody))
	assert.Equal(t, "response-body", string(snapshot.UpstreamResponseBody))
}

// TestConversationCaptureWriterRecordsOnlyWrittenBytes verifies failed client writes do not record unsent bytes.
func TestConversationCaptureWriterRecordsOnlyWrittenBytes(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	capture := NewConversationCapture()
	writer := &conversationCaptureWriter{
		ResponseWriter: &shortConversationResponseWriter{ResponseWriter: context.Writer, limit: 3},
		capture:        capture,
	}

	n, err := writer.Write([]byte("abcdef"))
	assert.Equal(t, 3, n)
	assert.ErrorIs(t, err, io.ErrShortWrite)
	n, err = writer.WriteString("ghijkl")
	assert.Equal(t, 3, n)
	assert.ErrorIs(t, err, io.ErrShortWrite)

	snapshot := capture.Snapshot()
	assert.Equal(t, "abcghi", string(snapshot.ClientResponseBody))
	assert.EqualValues(t, 6, snapshot.ClientResponseBytes)
}
