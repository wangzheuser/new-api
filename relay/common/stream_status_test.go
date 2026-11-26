package common

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamFirstPayloadBudget checks retry, cancellation and long-stream boundaries on a virtual clock.
func TestStreamFirstPayloadBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deadline := time.Now().Add(2 * time.Second)
		status := NewStreamStatus()
		var writer sync.Mutex
		client := t.Context()
		first, cancelFirst := context.WithCancel(client)
		defer cancelFirst()
		status.WatchFirstPayload(deadline, &writer, cancelFirst)
		time.Sleep(time.Second)
		require.False(t, status.FinishFirstPayloadWait())
		status.PrepareRetryAttempt()
		second, cancelSecond := context.WithCancel(client)
		defer cancelSecond()
		status.WatchFirstPayload(deadline, &writer, cancelSecond)
		status.MarkAppHTTPCommitted()
		status.MarkClientEventCommitted(false) // Responses metadata already commits its historical billing boundary.
		require.True(t, status.ClientPayloadIsCommitted())
		require.Zero(t, status.FirstMeaningfulByteDuration(deadline.Add(-2*time.Second)))
		time.Sleep(time.Second)
		synctest.Wait()
		require.ErrorIs(t, second.Err(), context.Canceled)
		require.NoError(t, first.Err())
		require.NoError(t, client.Err())
		assert.True(t, status.FinishFirstPayloadWait())
		reason, err := status.End()
		assert.Equal(t, StreamEndReasonTimeout, reason)
		assert.ErrorIs(t, err, ErrFirstPayloadTimeout)

		for _, finish := range []func(*StreamStatus){
			(*StreamStatus).MarkClientPayloadCommitted,
			func(s *StreamStatus) { s.SetEndReason(StreamEndReasonDone, nil) },
		} {
			stream, cancel := context.WithCancel(client)
			status = NewStreamStatus()
			status.WatchFirstPayload(time.Now().Add(time.Second), &writer, cancel)
			finish(status)
			time.Sleep(time.Hour)
			synctest.Wait()
			assert.NoError(t, stream.Err())
			assert.False(t, status.FinishFirstPayloadWait())
			cancel()
		}
	})
}

func TestStreamFirstPayloadRetryLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		status := NewStreamStatus()
		var writer sync.Mutex
		first, cancelFirst := context.WithCancel(t.Context())
		defer cancelFirst()
		status.WatchFirstPayload(time.Now().Add(time.Second), &writer, cancelFirst)
		time.Sleep(time.Second)
		synctest.Wait()
		require.ErrorIs(t, first.Err(), context.Canceled)
		require.True(t, status.FinishFirstPayloadWait())
		status.PrepareRetryAttempt()
		second, cancelSecond := context.WithCancel(t.Context())
		defer cancelSecond()
		status.WatchFirstPayload(time.Now().Add(2*time.Second), &writer, cancelSecond)
		time.Sleep(time.Second)
		synctest.Wait()
		require.NoError(t, second.Err())
		time.Sleep(time.Second)
		synctest.Wait()
		require.ErrorIs(t, second.Err(), context.Canceled)
		require.True(t, status.FinishFirstPayloadWait())
		status.SetEndReason(StreamEndReasonPingFail, fmt.Errorf("closed"))
		status.PrepareRetryAttempt()
		assert.True(t, status.RetryBlocked(false), "a later client failure survives timeout and retry reset")
		reason, err := status.End()
		assert.Equal(t, StreamEndReasonTimeout, reason)
		assert.ErrorIs(t, err, ErrFirstPayloadTimeout)
	})
}

func TestStreamStatus_SetEndReason_FirstWins(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonTimeout, nil)
	s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))

	assert.Equal(t, StreamEndReasonDone, s.EndReason)
	assert.Nil(t, s.EndError)
}

func TestStreamStatus_PartialUsageFillsMissingFieldsThenPrefersUpstream(t *testing.T) {
	t.Parallel()
	status := NewStreamStatus()

	status.ObservePartialUsage(&dto.Usage{PromptTokens: 12, TotalTokens: 12}, true, false)
	status.ObservePartialUsage(&dto.Usage{PromptTokens: 12, CompletionTokens: 5, TotalTokens: 17}, false, true)

	usage, ok, estimated := status.PartialUsageSnapshot()
	require.True(t, ok)
	assert.True(t, estimated)
	assert.Equal(t, 12, usage.PromptTokens)
	assert.Equal(t, 5, usage.CompletionTokens)
	assert.Equal(t, 17, usage.TotalTokens)

	status.ObservePartialUsage(&dto.Usage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16}, true, false)
	usage, ok, estimated = status.PartialUsageSnapshot()
	require.True(t, ok)
	assert.False(t, estimated)
	assert.Equal(t, 4, usage.CompletionTokens, "authoritative upstream usage replaces the local estimate")
	assert.Equal(t, 16, usage.TotalTokens)

	// A retry before the first payload must not inherit the first attempt's input or failure.
	status.MarkFailed("incomplete", "api_error", 502)
	status.RecordError("stream interrupted")
	status.PrepareRetryAttempt()
	status.ObservePartialUsage(&dto.Usage{PromptTokens: 3, CompletionTokens: 1}, true, false)
	usage, ok, _ = status.PartialUsageSnapshot()
	require.True(t, ok)
	assert.Equal(t, 3, usage.PromptTokens)
	assert.False(t, status.HasErrors())
	assert.False(t, status.ResponseFailed())
}

func TestStreamStatus_FirstMeaningfulByteRecordedOnce(t *testing.T) {
	t.Parallel()
	status := NewStreamStatus()
	start := time.Now().Add(-time.Second)

	status.MarkClientPayloadCommitted()
	first := status.FirstMeaningfulByteDuration(start)
	status.MarkClientPayloadCommitted()

	assert.Greater(t, first, time.Duration(0))
	assert.Equal(t, first, status.FirstMeaningfulByteDuration(start))
}

func TestStreamStatus_ToolPayloadBytesAccumulateAndRemainConcurrentSafe(t *testing.T) {
	t.Parallel()
	status := NewStreamStatus()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status.ObserveToolPayloadBytes(2, 3)
		}()
	}
	wg.Wait()

	nameBytes, argumentBytes := status.ToolPayloadBytes()
	assert.Equal(t, int64(40), nameBytes)
	assert.Equal(t, int64(60), argumentBytes)
}

func TestStreamStatus_CommitErrorAndBillingFinalizationAreIdempotent(t *testing.T) {
	t.Parallel()
	status := NewStreamStatus()

	status.MarkAppHTTPCommitted()
	status.MarkClientPayloadCommitted()
	assert.True(t, status.AppHTTPIsCommitted())
	assert.True(t, status.ClientPayloadIsCommitted())
	assert.True(t, status.TryMarkErrorFrameWritten())
	assert.False(t, status.TryMarkErrorFrameWritten())
	assert.True(t, status.SetBillingFinalization(BillingSettledPartial))
	assert.False(t, status.SetBillingFinalization(BillingRefunded))
	assert.Equal(t, BillingSettledPartial, status.GetBillingFinalization())
}

func TestStreamStatus_RetryCommitPolicyDefaultsToHTTPAndAcceptsPayload(t *testing.T) {
	t.Parallel()
	status := NewStreamStatus()

	assert.Equal(t, StreamRetryCommitPolicyHTTP, status.RetryCommitPolicy())
	assert.False(t, status.RetryBlocked(false))
	assert.True(t, status.RetryBlocked(true))
	status.SetRetryCommitPolicy(StreamRetryCommitPolicyPayload)
	assert.Equal(t, StreamRetryCommitPolicyPayload, status.RetryCommitPolicy())
	assert.False(t, status.RetryBlocked(true), "SSE headers alone must not block payload-aware retry")
	status.MarkClientPayloadCommitted()
	assert.True(t, status.RetryBlocked(true))
	status.SetRetryCommitPolicy("unknown")
	assert.Equal(t, StreamRetryCommitPolicyHTTP, status.RetryCommitPolicy())
}

func TestStreamStatus_SetEndReason_WithError(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	expectedErr := fmt.Errorf("read: connection reset")
	s.SetEndReason(StreamEndReasonScannerErr, expectedErr)

	assert.Equal(t, StreamEndReasonScannerErr, s.EndReason)
	assert.Equal(t, expectedErr, s.EndError)
}

func TestStreamStatus_SetEndReason_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	s.SetEndReason(StreamEndReasonDone, nil)
}

func TestStreamStatus_SetEndReason_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	reasons := []StreamEndReason{
		StreamEndReasonDone,
		StreamEndReasonTimeout,
		StreamEndReasonClientGone,
		StreamEndReasonScannerErr,
		StreamEndReasonHandlerStop,
		StreamEndReasonEOF,
		StreamEndReasonPanic,
		StreamEndReasonPingFail,
	}

	var wg sync.WaitGroup
	for _, r := range reasons {
		wg.Add(1)
		go func(reason StreamEndReason) {
			defer wg.Done()
			s.SetEndReason(reason, nil)
		}(r)
	}
	wg.Wait()

	assert.NotEqual(t, StreamEndReasonNone, s.EndReason)
}

func TestStreamStatus_RecordError_Basic(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.RecordError("bad json")
	s.RecordError("another bad json")
	s.RecordError("client gone")

	assert.True(t, s.HasErrors())
	assert.Equal(t, 3, s.TotalErrorCount())
	assert.Len(t, s.Errors, 3)
}

func TestStreamStatus_RecordError_CapAtMax(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	for i := range 30 {
		s.RecordError(fmt.Sprintf("error_%d", i))
	}

	assert.Equal(t, maxStreamErrorEntries, len(s.Errors))
	assert.Equal(t, 30, s.TotalErrorCount())
}

func TestStreamStatus_RecordError_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	s.RecordError("should not panic")
}

func TestStreamStatus_RecordError_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.RecordError(fmt.Sprintf("error_%d", idx))
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 100, s.TotalErrorCount())
	assert.LessOrEqual(t, len(s.Errors), maxStreamErrorEntries)
}

func TestStreamStatus_HasErrors_Empty(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_HasErrors_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_IsNormalEnd(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason StreamEndReason
		normal bool
	}{
		{StreamEndReasonDone, true},
		{StreamEndReasonEOF, true},
		{StreamEndReasonHandlerStop, true},
		{StreamEndReasonTimeout, false},
		{StreamEndReasonClientGone, false},
		{StreamEndReasonScannerErr, false},
		{StreamEndReasonUnexpectedEOF, false},
		{StreamEndReasonPanic, false},
		{StreamEndReasonPingFail, false},
		{StreamEndReasonNone, false},
	}
	for _, tt := range tests {
		s := NewStreamStatus()
		s.SetEndReason(tt.reason, nil)
		assert.Equal(t, tt.normal, s.IsNormalEnd(), "reason=%s", tt.reason)
	}
}

func TestStreamStatus_IsNormalEnd_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.True(t, s.IsNormalEnd())
}

func TestStreamStatus_Summary(t *testing.T) {
	t.Parallel()

	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	summary := s.Summary()
	assert.Contains(t, summary, "reason=done")
	assert.NotContains(t, summary, "soft_errors")

	s2 := NewStreamStatus()
	s2.SetEndReason(StreamEndReasonTimeout, nil)
	s2.RecordError("bad json")
	s2.RecordError("write failed")
	summary2 := s2.Summary()
	assert.Contains(t, summary2, "reason=timeout")
	assert.Contains(t, summary2, "soft_errors=2")
}

func TestStreamStatus_Terminal(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.SetTerminal("response.incomplete", "incomplete")
	event, status := s.Terminal()

	assert.Equal(t, "response.incomplete", event)
	assert.Equal(t, "incomplete", status)
	assert.Contains(t, s.Summary(), `terminal_event="response.incomplete"`)
	assert.Contains(t, s.Summary(), `terminal_status="incomplete"`)
}

func TestStreamStatus_Summary_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.Equal(t, "StreamStatus<nil>", s.Summary())
}
