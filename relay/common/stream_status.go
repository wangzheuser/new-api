package common

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

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

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endOnce   sync.Once
	endMu     sync.RWMutex

	mu             sync.Mutex
	Errors         []StreamErrorEntry
	ErrorCount     int
	terminalEvent  string
	terminalStatus string
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.endMu.Lock()
		defer s.endMu.Unlock()
		s.EndReason = reason
		s.EndError = err
	})
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
