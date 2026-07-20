package common

import (
	"io"
	"net/http"
	"strings"
	"sync"

	basecommon "github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const conversationCaptureSegmentLimit = 2 * 1024 * 1024

const conversationBaseWriterKey = "conversation_base_writer"

type captureSegment struct {
	Data      []byte
	Total     int64
	Truncated bool
}

// ConversationCapture holds bounded copies of one relay attempt.
type ConversationCapture struct {
	mu               sync.Mutex
	clientRequest    captureSegment
	upstreamRequest  captureSegment
	upstreamResponse captureSegment
	clientResponse   captureSegment
}

// ConversationCaptureSnapshot is an immutable copy ready for persistence.
type ConversationCaptureSnapshot struct {
	ClientRequestBody         []byte `json:"-"`
	UpstreamRequestBody       []byte `json:"-"`
	UpstreamResponseBody      []byte `json:"-"`
	ClientResponseBody        []byte `json:"-"`
	ClientRequestBytes        int64  `json:"client_request_bytes"`
	UpstreamRequestBytes      int64  `json:"upstream_request_bytes"`
	UpstreamResponseBytes     int64  `json:"upstream_response_bytes"`
	ClientResponseBytes       int64  `json:"client_response_bytes"`
	ClientRequestTruncated    bool   `json:"client_request_truncated"`
	UpstreamRequestTruncated  bool   `json:"upstream_request_truncated"`
	UpstreamResponseTruncated bool   `json:"upstream_response_truncated"`
	ClientResponseTruncated   bool   `json:"client_response_truncated"`
}

// NewConversationCapture creates an empty bounded capture.
func NewConversationCapture() *ConversationCapture {
	return &ConversationCapture{}
}

// StartConversationCapture resets capture state for the currently selected channel.
func StartConversationCapture(c *gin.Context, info *RelayInfo) {
	if c == nil || info == nil {
		return
	}
	baseWriter, ok := c.Get(conversationBaseWriterKey)
	if !ok {
		baseWriter = c.Writer
		c.Set(conversationBaseWriterKey, baseWriter)
	}
	if writer, ok := baseWriter.(gin.ResponseWriter); ok {
		c.Writer = writer
	}
	info.ConversationCapture = nil

	if !basecommon.ConversationCaptureEnabled ||
		basecommon.UsingLogDatabase(basecommon.DatabaseTypeClickHouse) ||
		info.ChannelMeta == nil || !info.ChannelOtherSettings.ConversationLogEnabled ||
		!isConversationCaptureEligible(info) {
		return
	}

	capture := NewConversationCapture()
	if storage, err := basecommon.GetBodyStorage(c); err == nil {
		current, seekErr := storage.Seek(0, io.SeekCurrent)
		if seekErr == nil {
			if _, seekErr = storage.Seek(0, io.SeekStart); seekErr == nil {
				data, _ := io.ReadAll(io.LimitReader(storage, conversationCaptureSegmentLimit+1))
				capture.set(&capture.clientRequest, data, storage.Size())
			}
			_, _ = storage.Seek(current, io.SeekStart)
		}
	}
	info.ConversationCapture = capture
	if writer, ok := baseWriter.(gin.ResponseWriter); ok {
		c.Writer = &conversationCaptureWriter{ResponseWriter: writer, capture: capture}
	}
}

// isConversationCaptureEligible limits capture to supported text relay formats.
func isConversationCaptureEligible(info *RelayInfo) bool {
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		return true
	case types.RelayFormatOpenAI:
		return info.RelayMode == relayconstant.RelayModeChatCompletions ||
			info.RelayMode == relayconstant.RelayModeCompletions
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return true
	case types.RelayFormatGemini:
		return strings.Contains(info.RequestURLPath, "generateContent")
	default:
		return false
	}
}

// Snapshot returns a thread-safe copy of captured data and truncation metadata.
func (capture *ConversationCapture) Snapshot() ConversationCaptureSnapshot {
	if capture == nil {
		return ConversationCaptureSnapshot{}
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return ConversationCaptureSnapshot{
		ClientRequestBody:         cloneCaptureBytes(capture.clientRequest.Data),
		UpstreamRequestBody:       cloneCaptureBytes(capture.upstreamRequest.Data),
		UpstreamResponseBody:      cloneCaptureBytes(capture.upstreamResponse.Data),
		ClientResponseBody:        cloneCaptureBytes(capture.clientResponse.Data),
		ClientRequestBytes:        capture.clientRequest.Total,
		UpstreamRequestBytes:      capture.upstreamRequest.Total,
		UpstreamResponseBytes:     capture.upstreamResponse.Total,
		ClientResponseBytes:       capture.clientResponse.Total,
		ClientRequestTruncated:    capture.clientRequest.Truncated,
		UpstreamRequestTruncated:  capture.upstreamRequest.Truncated,
		UpstreamResponseTruncated: capture.upstreamResponse.Truncated,
		ClientResponseTruncated:   capture.clientResponse.Truncated,
	}
}

// set replaces one segment while preserving the original byte count.
func (capture *ConversationCapture) set(segment *captureSegment, data []byte, total int64) {
	if capture == nil {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	segment.Total = total
	if len(data) > conversationCaptureSegmentLimit {
		data = data[:conversationCaptureSegmentLimit]
	}
	segment.Data = append(segment.Data[:0], data...)
	segment.Truncated = total > int64(len(segment.Data))
}

// append adds bytes to one segment without exceeding the memory limit.
func (capture *ConversationCapture) append(segment *captureSegment, data []byte) {
	if capture == nil || len(data) == 0 {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	segment.Total += int64(len(data))
	remaining := conversationCaptureSegmentLimit - len(segment.Data)
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		segment.Data = append(segment.Data, data...)
	}
	segment.Truncated = segment.Total > int64(len(segment.Data))
}

// cloneCaptureBytes returns a detached copy safe for asynchronous persistence.
func cloneCaptureBytes(data []byte) []byte {
	return append([]byte(nil), data...)
}

// WrapConversationUpstreamRequest captures bytes as the HTTP client sends them.
func WrapConversationUpstreamRequest(info *RelayInfo, req *http.Request) {
	if info == nil || info.ConversationCapture == nil || req == nil || req.Body == nil {
		return
	}
	req.Body = &conversationCaptureReadCloser{
		ReadCloser: req.Body,
		append: func(data []byte) {
			info.ConversationCapture.append(&info.ConversationCapture.upstreamRequest, data)
		},
	}
}

// WrapConversationUpstreamResponse captures bytes as the adaptor reads them.
func WrapConversationUpstreamResponse(info *RelayInfo, resp *http.Response) {
	if info == nil || info.ConversationCapture == nil || resp == nil || resp.Body == nil {
		return
	}
	resp.Body = &conversationCaptureReadCloser{
		ReadCloser: resp.Body,
		append: func(data []byte) {
			info.ConversationCapture.append(&info.ConversationCapture.upstreamResponse, data)
		},
	}
}

type conversationCaptureReadCloser struct {
	io.ReadCloser
	append func([]byte)
}

// Read forwards data and records the bytes consumed by the caller.
func (reader *conversationCaptureReadCloser) Read(p []byte) (int, error) {
	n, err := reader.ReadCloser.Read(p)
	if n > 0 {
		reader.append(p[:n])
	}
	return n, err
}

type conversationCaptureWriter struct {
	gin.ResponseWriter
	capture *ConversationCapture
}

// Write captures binary response chunks before forwarding them to Gin.
func (writer *conversationCaptureWriter) Write(data []byte) (int, error) {
	n, err := writer.ResponseWriter.Write(data)
	writer.capture.append(&writer.capture.clientResponse, data[:n])
	return n, err
}

// WriteString captures string response chunks before forwarding them to Gin.
func (writer *conversationCaptureWriter) WriteString(data string) (int, error) {
	n, err := writer.ResponseWriter.WriteString(data)
	writer.capture.append(&writer.capture.clientResponse, []byte(data[:n]))
	return n, err
}
