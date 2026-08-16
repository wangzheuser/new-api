package common

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const responseOverrideBufferContextKey = "response_override_buffer"

const finalResponseWriterFactoryContextKey = "final_response_writer_factory"

const maxResponseOverrideBufferBytes = 32 << 20

// FinalResponseWriter is a stateful client-response transform installed above
// the attempt-scoped response buffer.
type FinalResponseWriter interface {
	gin.ResponseWriter
	RebindResponseWriter(writer gin.ResponseWriter)
	FinishResponseWriter(commit bool) error
}

const (
	ResponseOverrideNotAppliedStreaming    = "streaming"
	ResponseOverrideNotAppliedNoMatch      = "no_match"
	ResponseOverrideNotAppliedConfigError  = "config_error"
	ResponseOverrideNotAppliedRelayError   = "relay_error"
	ResponseOverrideNotAppliedNotEvaluated = "not_evaluated"
	ResponseOverrideNotAppliedBufferLimit  = "buffer_limit_exceeded"
)

// ResponseOverrideDecision records the response rule decision independently
// from the upstream relay result and its billing lifecycle.
type ResponseOverrideDecision struct {
	Configured           bool               `json:"configured"`
	Evaluated            bool               `json:"evaluated"`
	Applied              bool               `json:"applied"`
	NotAppliedReason     string             `json:"not_applied_reason,omitempty"`
	RuleID               string             `json:"rule_id,omitempty"`
	RuleIndex            int                `json:"rule_index,omitempty"`
	Description          string             `json:"description,omitempty"`
	ChannelID            int                `json:"channel_id,omitempty"`
	ChannelName          string             `json:"channel_name,omitempty"`
	UpstreamStatusCode   int                `json:"upstream_status_code,omitempty"`
	CandidateStatusCode  int                `json:"candidate_status_code,omitempty"`
	ClientStatusCode     int                `json:"client_status_code,omitempty"`
	Semantics            ResponseSemantics  `json:"semantics"`
	Billable             bool               `json:"billable"`
	Retryable            bool               `json:"retryable"`
	AffectsChannelHealth bool               `json:"affects_channel_health"`
	ConfigError          string             `json:"config_error,omitempty"`
	ClientError          *types.NewAPIError `json:"-"`
}

// ResponseOverrideBuffer delays one non-streaming response until response
// override rules have been evaluated.
type ResponseOverrideBuffer struct {
	gin.ResponseWriter

	mu         sync.Mutex
	context    *gin.Context
	info       *RelayInfo
	header     http.Header
	statusCode int
	body       []byte
	written    bool
	released   bool
}

// StartResponseOverrideBuffer installs an attempt-scoped response buffer when
// the selected channel has response override rules.
func StartResponseOverrideBuffer(c *gin.Context, info *RelayInfo) {
	if info != nil {
		info.ResponseOverride = nil
		info.ResponseSemantics = ResponseSemantics{}
	}
	if c == nil || c.Writer == nil || info == nil || info.ChannelMeta == nil ||
		!isConversationCaptureEligible(info) || !HasResponseOverride(info.ChannelMeta.ParamOverride) {
		if c != nil {
			c.Set(responseOverrideBufferContextKey, nil)
		}
		return
	}

	info.ResponseOverride = newResponseOverrideDecision(info.ChannelMeta)
	if info.IsStream {
		info.ResponseOverride.NotAppliedReason = ResponseOverrideNotAppliedStreaming
		info.ResponseOverride.Semantics = info.ResponseSemantics
		c.Set(responseOverrideBufferContextKey, nil)
		return
	}

	buffer := &ResponseOverrideBuffer{
		ResponseWriter: c.Writer,
		context:        c,
		info:           info,
		header:         cloneResponseHeader(c.Writer.Header()),
		statusCode:     http.StatusOK,
	}
	c.Writer = buffer
	c.Set(responseOverrideBufferContextKey, buffer)
}

// SetFinalResponseWriterFactory registers an attempt-level transform that must
// run before response override evaluates the final client JSON.
func SetFinalResponseWriterFactory(c *gin.Context, factory func(gin.ResponseWriter) FinalResponseWriter) {
	if c == nil {
		return
	}
	if factory == nil {
		c.Set(finalResponseWriterFactoryContextKey, nil)
		return
	}
	c.Set(finalResponseWriterFactoryContextKey, factory)
}

// ApplyFinalResponseWriter installs the registered client-body transform over
// the current attempt's buffer and capture chain.
func ApplyFinalResponseWriter(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	value, exists := c.Get(finalResponseWriterFactoryContextKey)
	if !exists || value == nil {
		return
	}
	factory, ok := value.(func(gin.ResponseWriter) FinalResponseWriter)
	if !ok {
		return
	}
	if writer := factory(c.Writer); writer != nil {
		c.Writer = writer
	}
}

// CurrentResponseOverrideBuffer returns the buffer installed for the current
// relay attempt, if response override evaluation is enabled.
func CurrentResponseOverrideBuffer(c *gin.Context) *ResponseOverrideBuffer {
	if c == nil {
		return nil
	}
	value, exists := c.Get(responseOverrideBufferContextKey)
	if !exists || value == nil {
		return nil
	}
	buffer, _ := value.(*ResponseOverrideBuffer)
	return buffer
}

// EvaluateResponseOverride evaluates the buffered response before quota
// settlement and leaves commit or replacement to the relay controller.
func EvaluateResponseOverride(c *gin.Context, upstreamStatusCode int) *ResponseOverrideDecision {
	buffer := CurrentResponseOverrideBuffer(c)
	if buffer == nil {
		return nil
	}
	return buffer.Evaluate(upstreamStatusCode)
}

// Header returns isolated candidate headers until the buffer is released.
func (b *ResponseOverrideBuffer) Header() http.Header {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	released := b.released
	header := b.header
	writer := b.ResponseWriter
	b.mu.Unlock()
	if released {
		return writer.Header()
	}
	return header
}

// WriteHeader records the candidate status without committing it downstream.
func (b *ResponseOverrideBuffer) WriteHeader(code int) {
	if b == nil {
		return
	}
	if b.shouldReleaseForStreaming() {
		_ = b.release(true, false)
		b.ResponseWriter.WriteHeader(code)
		return
	}

	b.mu.Lock()
	if b.released {
		writer := b.ResponseWriter
		b.mu.Unlock()
		writer.WriteHeader(code)
		return
	}
	defer b.mu.Unlock()
	if b.written {
		return
	}
	b.statusCode = code
	b.written = true
}

// WriteHeaderNow records the default status without committing it downstream.
func (b *ResponseOverrideBuffer) WriteHeaderNow() {
	b.WriteHeader(b.Status())
}

// Write buffers candidate response bytes without committing them downstream.
func (b *ResponseOverrideBuffer) Write(data []byte) (int, error) {
	if b == nil {
		return 0, http.ErrBodyNotAllowed
	}
	if b.shouldReleaseForStreaming() {
		if err := b.release(true, false); err != nil {
			return 0, err
		}
		return b.ResponseWriter.Write(data)
	}

	b.mu.Lock()
	if b.released {
		writer := b.ResponseWriter
		b.mu.Unlock()
		return writer.Write(data)
	}
	if !b.written {
		b.written = true
	}
	if len(b.body)+len(data) > maxResponseOverrideBufferBytes {
		if b.info != nil && b.info.ResponseOverride != nil {
			b.info.ResponseOverride.NotAppliedReason = ResponseOverrideNotAppliedBufferLimit
			b.info.ResponseOverride.Semantics = b.info.ResponseSemantics
		}
		b.mu.Unlock()
		if err := b.release(true, false); err != nil {
			return 0, err
		}
		return b.ResponseWriter.Write(data)
	}
	b.body = append(b.body, data...)
	b.mu.Unlock()
	return len(data), nil
}

// WriteString buffers candidate response text without committing it downstream.
func (b *ResponseOverrideBuffer) WriteString(data string) (int, error) {
	return b.Write([]byte(data))
}

// Flush releases an unexpectedly streaming response and otherwise keeps the
// non-streaming candidate uncommitted.
func (b *ResponseOverrideBuffer) Flush() {
	if b == nil || !b.shouldReleaseForStreaming() {
		return
	}
	_ = b.release(true, false)
	b.ResponseWriter.Flush()
}

// Hijack delegates connection takeover to the wrapped Gin writer.
func (b *ResponseOverrideBuffer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if b == nil || b.ResponseWriter == nil {
		return nil, nil, fmt.Errorf("response writer is nil")
	}
	return b.ResponseWriter.Hijack()
}

// CloseNotify delegates the legacy client-disconnect signal to the wrapped Gin writer.
func (b *ResponseOverrideBuffer) CloseNotify() <-chan bool {
	if b == nil || b.ResponseWriter == nil {
		closed := make(chan bool)
		close(closed)
		return closed
	}
	return b.ResponseWriter.CloseNotify()
}

// Pusher delegates HTTP/2 server push support to the wrapped Gin writer.
func (b *ResponseOverrideBuffer) Pusher() http.Pusher {
	if b == nil || b.ResponseWriter == nil {
		return nil
	}
	return b.ResponseWriter.Pusher()
}

// Status returns the candidate status while buffering.
func (b *ResponseOverrideBuffer) Status() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return b.ResponseWriter.Status()
	}
	return b.statusCode
}

// Size returns the number of candidate response bytes buffered so far.
func (b *ResponseOverrideBuffer) Size() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return b.ResponseWriter.Size()
	}
	return len(b.body)
}

// Written reports whether bytes have been committed downstream.
func (b *ResponseOverrideBuffer) Written() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return b.ResponseWriter.Written()
	}
	return false
}

// Snapshot returns detached candidate response data for diagnostics and tests.
func (b *ResponseOverrideBuffer) Snapshot() (int, http.Header, []byte) {
	if b == nil {
		return 0, nil, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.statusCode, cloneResponseHeader(b.header), append([]byte(nil), b.body...)
}

// Evaluate applies response rules to the complete candidate response.
func (b *ResponseOverrideBuffer) Evaluate(upstreamStatusCode int) *ResponseOverrideDecision {
	if b == nil || b.info == nil || b.info.ResponseOverride == nil {
		return nil
	}
	if err := finishFinalResponseWriter(b.context, true); err != nil {
		decision := b.info.ResponseOverride
		decision.Evaluated = true
		decision.NotAppliedReason = ResponseOverrideNotAppliedConfigError
		decision.ConfigError = err.Error()
		return decision
	}

	b.mu.Lock()
	statusCode := b.statusCode
	header := cloneResponseHeader(b.header)
	body := append([]byte(nil), b.body...)
	released := b.released
	b.mu.Unlock()

	decision := b.info.ResponseOverride
	if decision.Evaluated || released {
		return decision
	}
	result, err := ApplyResponseOverride(b.info.ChannelMeta.ParamOverride, b.info, BufferedRelayResponse{
		Body:                body,
		UpstreamStatusCode:  upstreamStatusCode,
		CandidateStatusCode: statusCode,
		Headers:             header,
		UpstreamFormat:      b.info.GetFinalRequestRelayFormat(),
		CandidateFormat:     b.info.RelayFormat,
	})

	decision.Evaluated = true
	decision.UpstreamStatusCode = upstreamStatusCode
	decision.CandidateStatusCode = statusCode
	decision.ClientStatusCode = statusCode
	decision.Semantics = b.info.ResponseSemantics
	if err != nil {
		decision.NotAppliedReason = ResponseOverrideNotAppliedConfigError
		decision.ConfigError = err.Error()
		return decision
	}
	if result.Disposition == ResponseOverridePass {
		decision.NotAppliedReason = ResponseOverrideNotAppliedNoMatch
		return decision
	}

	decision.Applied = true
	decision.RuleID = result.RuleID
	decision.RuleIndex = result.RuleIndex
	decision.Description = result.Description
	decision.ClientError = NewAPIErrorFromParamOverride(result.Error)
	decision.ClientStatusCode = decision.ClientError.StatusCode
	decision.Semantics.Client.HTTPStatus = decision.ClientStatusCode
	b.info.ResponseSemantics.Client.HTTPStatus = decision.ClientStatusCode
	return decision
}

// Commit writes the buffered candidate response to the original downstream
// writer and restores it as the active Gin writer.
func (b *ResponseOverrideBuffer) Commit(c *gin.Context) error {
	if b == nil {
		return nil
	}
	if b.info != nil && b.info.ResponseOverride != nil && !b.info.ResponseOverride.Evaluated {
		b.info.ResponseOverride.NotAppliedReason = ResponseOverrideNotAppliedNotEvaluated
		b.info.ResponseOverride.CandidateStatusCode = b.Status()
		b.info.ResponseOverride.ClientStatusCode = b.Status()
		b.info.ResponseOverride.Semantics = b.info.ResponseSemantics
	}
	if err := finishFinalResponseWriter(c, true); err != nil {
		return err
	}
	return b.release(true, false)
}

// Discard drops the candidate response and restores the original writer.
func (b *ResponseOverrideBuffer) Discard(c *gin.Context) {
	if b == nil {
		return
	}
	_ = finishFinalResponseWriter(c, false)
	_ = b.release(false, false)
	clearDiscardedResponseHeaders(b.ResponseWriter.Header())
}

// MarkRelayError records why an unevaluated candidate was discarded.
func (b *ResponseOverrideBuffer) MarkRelayError() {
	if b == nil || b.info == nil || b.info.ResponseOverride == nil || b.info.ResponseOverride.Evaluated {
		return
	}
	b.info.ResponseOverride.NotAppliedReason = ResponseOverrideNotAppliedRelayError
	b.info.ResponseOverride.Semantics = b.info.ResponseSemantics
}

func (b *ResponseOverrideBuffer) shouldReleaseForStreaming() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return false
	}
	return b.shouldMarkStreaming()
}

func (b *ResponseOverrideBuffer) release(commit, flush bool) error {
	b.mu.Lock()
	if b.released {
		b.mu.Unlock()
		return nil
	}
	b.released = true
	statusCode := b.statusCode
	header := cloneResponseHeader(b.header)
	body := append([]byte(nil), b.body...)
	written := b.written
	b.mu.Unlock()

	if commit && b.info != nil && b.info.ResponseOverride != nil && b.shouldMarkStreaming() {
		decision := b.info.ResponseOverride
		decision.NotAppliedReason = ResponseOverrideNotAppliedStreaming
		decision.Semantics = b.info.ResponseSemantics
	}
	restoreResponseOverrideWriter(b.context, b)
	if !commit {
		return nil
	}
	replaceResponseHeader(b.ResponseWriter.Header(), header)
	if written || len(body) > 0 {
		b.ResponseWriter.WriteHeader(statusCode)
	}
	if len(body) > 0 {
		if _, err := b.ResponseWriter.Write(body); err != nil {
			return err
		}
	}
	if flush {
		b.ResponseWriter.Flush()
	}
	return nil
}

func (b *ResponseOverrideBuffer) shouldMarkStreaming() bool {
	return (b.info != nil && b.info.IsStream) || strings.HasPrefix(strings.ToLower(b.header.Get("Content-Type")), "text/event-stream")
}

func newResponseOverrideDecision(channel *ChannelMeta) *ResponseOverrideDecision {
	decision := &ResponseOverrideDecision{
		Configured:           true,
		Billable:             true,
		Retryable:            false,
		AffectsChannelHealth: false,
	}
	if channel != nil {
		decision.ChannelID = channel.ChannelId
		decision.ChannelName = channel.ChannelName
	}
	return decision
}

func restoreResponseOverrideWriter(c *gin.Context, buffer *ResponseOverrideBuffer) {
	if c == nil {
		return
	}
	if buffer != nil {
		// Keep stateful transforms alive while their Write call is still active.
		if writer, ok := c.Writer.(FinalResponseWriter); ok {
			writer.RebindResponseWriter(buffer.ResponseWriter)
			c.Writer = writer
		} else {
			c.Writer = buffer.ResponseWriter
			ApplyFinalResponseWriter(c)
		}
	}
	c.Set(responseOverrideBufferContextKey, nil)
}

func finishFinalResponseWriter(c *gin.Context, commit bool) error {
	if c == nil || c.Writer == nil {
		return nil
	}
	writer, ok := c.Writer.(FinalResponseWriter)
	if !ok {
		return nil
	}
	return writer.FinishResponseWriter(commit)
}

func cloneResponseHeader(header http.Header) http.Header {
	if header == nil {
		return make(http.Header)
	}
	return header.Clone()
}

func replaceResponseHeader(target http.Header, source http.Header) {
	for key := range target {
		delete(target, key)
	}
	for key, values := range source {
		if strings.EqualFold(key, "Transfer-Encoding") {
			continue
		}
		target[key] = append([]string(nil), values...)
	}
}

func clearDiscardedResponseHeaders(header http.Header) {
	for _, key := range []string{
		"Content-Encoding",
		"Content-Length",
		"Content-Range",
		"Content-Type",
		"ETag",
		"Last-Modified",
		"Transfer-Encoding",
	} {
		header.Del(key)
	}
}
