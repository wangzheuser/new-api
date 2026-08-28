package common

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/dto"
)

type StreamEndReason string
type BillingFinalization string
type StreamRetryCommitPolicy string

const (
	StreamEndReasonNone          StreamEndReason = ""
	StreamEndReasonDone          StreamEndReason = "done"
	StreamEndReasonTimeout       StreamEndReason = "timeout"
	StreamEndReasonClientGone    StreamEndReason = "client_gone"
	StreamEndReasonScannerErr    StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop   StreamEndReason = "handler_stop"
	StreamEndReasonEOF           StreamEndReason = "eof"
	StreamEndReasonUnexpectedEOF StreamEndReason = "unexpected_eof"
	StreamEndReasonPanic         StreamEndReason = "panic"
	StreamEndReasonPingFail      StreamEndReason = "ping_fail"
)

const (
	BillingSettled        BillingFinalization = "settled"
	BillingSettledPartial BillingFinalization = "settled_partial"
	BillingRefunded       BillingFinalization = "refunded"
)

const (
	// StreamRetryCommitPolicyHTTP preserves the legacy rule that any committed HTTP response blocks retries.
	StreamRetryCommitPolicyHTTP StreamRetryCommitPolicy = "http"
	// StreamRetryCommitPolicyPayload allows retries until a meaningful payload or error frame reaches the client.
	StreamRetryCommitPolicyPayload StreamRetryCommitPolicy = "payload"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endSet    bool
	endMu     sync.RWMutex

	mu                       sync.Mutex
	Errors                   []StreamErrorEntry
	ErrorCount               int
	terminalEvent            string
	terminalStatus           string
	appHTTPCommitted         bool
	clientPayloadCommitted   bool
	errorFrameWritten        bool
	streamPolicyVersion      string
	retryCommitPolicy        StreamRetryCommitPolicy
	billingFinalization      BillingFinalization
	billingApplied           bool
	partialUsage             dto.Usage
	hasPartialUsage          bool
	hasUpstreamUsage         bool
	upstreamPromptUsage      bool
	upstreamCompletionUsage  bool
	upstreamInputUsage       bool
	upstreamOutputUsage      bool
	partialUsageEstimated    bool
	firstPayloadAt           time.Time
	partialToolNameBytes     int64
	partialToolArgumentBytes int64
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

// MarkAppHTTPCommitted records that APP already flushed the downstream status and SSE headers.
func (s *StreamStatus) MarkAppHTTPCommitted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.appHTTPCommitted = true
	s.mu.Unlock()
}

// AppHTTPIsCommitted reports whether the downstream HTTP response has been committed.
func (s *StreamStatus) AppHTTPIsCommitted() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appHTTPCommitted
}

// MarkClientPayloadCommitted records the first successfully flushed business payload.
func (s *StreamStatus) MarkClientPayloadCommitted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.clientPayloadCommitted {
		s.clientPayloadCommitted = true
		s.firstPayloadAt = time.Now()
	}
	s.mu.Unlock()
}

// FirstMeaningfulByteDuration returns the delay from request start to the first flushed payload.
func (s *StreamStatus) FirstMeaningfulByteDuration(start time.Time) time.Duration {
	if s == nil || start.IsZero() {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.firstPayloadAt.IsZero() || s.firstPayloadAt.Before(start) {
		return 0
	}
	return s.firstPayloadAt.Sub(start)
}

// ClientPayloadIsCommitted reports whether a meaningful payload reached the client writer.
func (s *StreamStatus) ClientPayloadIsCommitted() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientPayloadCommitted
}

// TryMarkErrorFrameWritten atomically reserves the unique downstream error terminal.
func (s *StreamStatus) TryMarkErrorFrameWritten() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errorFrameWritten {
		return false
	}
	s.errorFrameWritten = true
	return true
}

// ErrorFrameIsWritten reports whether an error terminal was already emitted.
func (s *StreamStatus) ErrorFrameIsWritten() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errorFrameWritten
}

// SetStreamPolicyVersion records the internal TARGET stream contract version.
func (s *StreamStatus) SetStreamPolicyVersion(version string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.streamPolicyVersion = strings.TrimSpace(version)
	s.mu.Unlock()
}

// StreamPolicyVersion returns the internal TARGET stream contract version.
func (s *StreamStatus) StreamPolicyVersion() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamPolicyVersion
}

// SetRetryCommitPolicy selects the downstream commit boundary used by transparent retries.
func (s *StreamStatus) SetRetryCommitPolicy(policy StreamRetryCommitPolicy) {
	if s == nil {
		return
	}
	if policy != StreamRetryCommitPolicyPayload {
		policy = StreamRetryCommitPolicyHTTP
	}
	s.mu.Lock()
	s.retryCommitPolicy = policy
	s.mu.Unlock()
}

// RetryCommitPolicy returns the downstream commit boundary used by transparent retries.
func (s *StreamStatus) RetryCommitPolicy() StreamRetryCommitPolicy {
	if s == nil {
		return StreamRetryCommitPolicyHTTP
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retryCommitPolicy == StreamRetryCommitPolicyPayload {
		return StreamRetryCommitPolicyPayload
	}
	return StreamRetryCommitPolicyHTTP
}

// RetryBlocked reports whether a retry could duplicate an already committed downstream response.
func (s *StreamStatus) RetryBlocked(httpCommitted bool) bool {
	if s == nil {
		return httpCommitted
	}
	reason, _ := s.End()
	if reason == StreamEndReasonClientGone || reason == StreamEndReasonPingFail {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payloadAware := s.retryCommitPolicy == StreamRetryCommitPolicyPayload || s.streamPolicyVersion == "progressive-v1"
	if payloadAware {
		return s.clientPayloadCommitted || s.errorFrameWritten
	}
	return httpCommitted
}

// SetBillingFinalization selects the unique settle/refund outcome.
func (s *StreamStatus) SetBillingFinalization(finalization BillingFinalization) bool {
	if s == nil || finalization == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.billingFinalization != "" {
		return false
	}
	s.billingFinalization = finalization
	return true
}

// GetBillingFinalization returns the selected billing outcome.
func (s *StreamStatus) GetBillingFinalization() BillingFinalization {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.billingFinalization
}

// TryBeginBillingApplication reserves the one financial/logging side effect for this stream.
func (s *StreamStatus) TryBeginBillingApplication() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.billingApplied {
		return false
	}
	s.billingApplied = true
	return true
}

// ObservePartialUsage stores monotonic upstream usage or a local estimate for partial settlement.
func (s *StreamStatus) ObservePartialUsage(usage *dto.Usage, upstream bool, estimated bool) {
	if s == nil || usage == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if upstream {
		if usage.PromptTokens > 0 {
			s.partialUsage.PromptTokens = max(s.partialUsage.PromptTokens, usage.PromptTokens)
			s.upstreamPromptUsage = true
		}
		if usage.CompletionTokens > 0 {
			if !s.upstreamCompletionUsage {
				s.partialUsage.CompletionTokens = usage.CompletionTokens
			} else {
				s.partialUsage.CompletionTokens = max(s.partialUsage.CompletionTokens, usage.CompletionTokens)
			}
			s.upstreamCompletionUsage = true
		}
		if usage.InputTokens > 0 {
			s.partialUsage.InputTokens = max(s.partialUsage.InputTokens, usage.InputTokens)
			s.upstreamInputUsage = true
		}
		if usage.OutputTokens > 0 {
			if !s.upstreamOutputUsage {
				s.partialUsage.OutputTokens = usage.OutputTokens
			} else {
				s.partialUsage.OutputTokens = max(s.partialUsage.OutputTokens, usage.OutputTokens)
			}
			s.upstreamOutputUsage = true
		}
		s.partialUsage.UsageSemantic = usage.UsageSemantic
		s.partialUsage.BillingUsage = usage.BillingUsage
		s.hasUpstreamUsage = true
	} else {
		if !s.upstreamPromptUsage {
			s.partialUsage.PromptTokens = max(s.partialUsage.PromptTokens, usage.PromptTokens)
		}
		if !s.upstreamCompletionUsage {
			s.partialUsage.CompletionTokens = max(s.partialUsage.CompletionTokens, usage.CompletionTokens)
		}
		if !s.upstreamInputUsage {
			s.partialUsage.InputTokens = max(s.partialUsage.InputTokens, usage.InputTokens)
		}
		if !s.upstreamOutputUsage {
			s.partialUsage.OutputTokens = max(s.partialUsage.OutputTokens, usage.OutputTokens)
		}
	}
	s.partialUsage.TotalTokens = max(
		usage.TotalTokens,
		s.partialUsage.PromptTokens+s.partialUsage.CompletionTokens,
		s.partialUsage.InputTokens+s.partialUsage.OutputTokens,
	)
	s.partialUsageEstimated = estimated &&
		(!s.upstreamPromptUsage || !s.upstreamCompletionUsage) &&
		(s.partialUsage.PromptTokens > 0 || s.partialUsage.CompletionTokens > 0)
	s.hasPartialUsage = true
}

// PartialUsageSnapshot returns the current usage, whether it exists, and whether any field is estimated.
func (s *StreamStatus) PartialUsageSnapshot() (dto.Usage, bool, bool) {
	if s == nil {
		return dto.Usage{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partialUsage, s.hasPartialUsage, s.partialUsageEstimated
}

// ObserveToolPayloadBytes records only tool fragments successfully flushed to the client.
func (s *StreamStatus) ObserveToolPayloadBytes(nameBytes int, argumentBytes int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.partialToolNameBytes += int64(max(0, nameBytes))
	s.partialToolArgumentBytes += int64(max(0, argumentBytes))
	s.mu.Unlock()
}

// ToolPayloadBytes returns flushed tool name and argument bytes for audit logs.
func (s *StreamStatus) ToolPayloadBytes() (int64, int64) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.partialToolNameBytes, s.partialToolArgumentBytes
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endMu.Lock()
	defer s.endMu.Unlock()
	if s.endSet {
		return
	}
	s.endSet = true
	s.EndReason = reason
	s.EndError = err
}

// PrepareRetryAttempt clears attempt-local terminal state without replacing the request-level status object.
func (s *StreamStatus) PrepareRetryAttempt() {
	if s == nil {
		return
	}
	s.endMu.Lock()
	if s.EndReason == StreamEndReasonClientGone || s.EndReason == StreamEndReasonPingFail {
		s.endMu.Unlock()
		return
	}
	s.endSet = false
	s.EndReason = StreamEndReasonNone
	s.EndError = nil
	s.endMu.Unlock()

	s.mu.Lock()
	s.terminalEvent = ""
	s.terminalStatus = ""
	s.streamPolicyVersion = ""
	s.retryCommitPolicy = StreamRetryCommitPolicyHTTP
	s.mu.Unlock()
}

// SetTerminal records the protocol-level event that ended the stream.
func (s *StreamStatus) SetTerminal(event string, status string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.terminalEvent = strings.TrimSpace(event)
	s.terminalStatus = strings.TrimSpace(status)
	s.mu.Unlock()
}

// Terminal returns the protocol terminal event and its semantic status.
func (s *StreamStatus) Terminal() (string, string) {
	if s == nil {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminalEvent, s.terminalStatus
}

// HasEnded reports whether an end reason has already been selected.
func (s *StreamStatus) HasEnded() bool {
	if s == nil {
		return false
	}
	s.endMu.RLock()
	defer s.endMu.RUnlock()
	return s.EndReason != StreamEndReasonNone
}

// End returns the selected stream end reason and error.
func (s *StreamStatus) End() (StreamEndReason, error) {
	if s == nil {
		return StreamEndReasonNone, nil
	}
	s.endMu.RLock()
	defer s.endMu.RUnlock()
	return s.EndReason, s.EndError
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

// ErrorMessages returns a stable snapshot of recorded soft stream errors.
func (s *StreamStatus) ErrorMessages() (int, []string) {
	if s == nil {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := make([]string, 0, len(s.Errors))
	for _, entry := range s.Errors {
		messages = append(messages, entry.Message)
	}
	return s.ErrorCount, messages
}

func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	s.endMu.RLock()
	reason := s.EndReason
	s.endMu.RUnlock()
	return reason == StreamEndReasonDone ||
		reason == StreamEndReasonEOF ||
		reason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	b := &strings.Builder{}
	s.endMu.RLock()
	reason := s.EndReason
	endError := s.EndError
	s.endMu.RUnlock()
	fmt.Fprintf(b, "reason=%s", reason)
	if endError != nil {
		fmt.Fprintf(b, " end_error=%q", endError.Error())
	}
	s.mu.Lock()
	if s.terminalEvent != "" {
		fmt.Fprintf(b, " terminal_event=%q", s.terminalEvent)
	}
	if s.terminalStatus != "" {
		fmt.Fprintf(b, " terminal_status=%q", s.terminalStatus)
	}
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	s.mu.Unlock()
	return b.String()
}
