package service

import (
	"errors"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendStreamStatusIncludesProtocolTerminalAndTransportEnd(t *testing.T) {
	streamStatus := relaycommon.NewStreamStatus()
	streamStatus.SetTerminal("response.failed", "failed")
	streamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, errors.New("mid stream aborted"))
	streamStatus.RecordError("malformed event")
	info := &relaycommon.RelayInfo{
		IsStream:              true,
		ReceivedResponseCount: 7,
		StreamStatus:          streamStatus,
	}
	other := map[string]interface{}{}

	AppendStreamStatus(info, other)

	streamInfo, ok := other["stream_status"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "error", streamInfo["status"])
	assert.Equal(t, "streaming", streamInfo["phase"])
	assert.Equal(t, "handler_stop", streamInfo["end_reason"])
	assert.Equal(t, "mid stream aborted", streamInfo["end_error"])
	assert.Equal(t, 7, streamInfo["received_event_count"])
	assert.Equal(t, true, streamInfo["terminal_seen"])
	assert.Equal(t, "response.failed", streamInfo["terminal_event"])
	assert.Equal(t, "failed", streamInfo["terminal_status"])
	assert.Equal(t, 1, streamInfo["error_count"])
	assert.Equal(t, []string{"malformed event"}, streamInfo["errors"])
}

func TestAppendStreamStatusMarksUnexpectedEOFWithoutTerminal(t *testing.T) {
	streamStatus := relaycommon.NewStreamStatus()
	streamStatus.SetEndReason(relaycommon.StreamEndReasonUnexpectedEOF, errors.New("stream ended before terminal event"))
	other := map[string]interface{}{}

	AppendStreamStatus(&relaycommon.RelayInfo{IsStream: true, StreamStatus: streamStatus}, other)

	streamInfo, ok := other["stream_status"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "error", streamInfo["status"])
	assert.Equal(t, "streaming", streamInfo["phase"])
	assert.Equal(t, "unexpected_eof", streamInfo["end_reason"])
	assert.Equal(t, false, streamInfo["terminal_seen"])
	assert.NotContains(t, streamInfo, "terminal_event")
}

func TestAppendStreamStatusMarksPreStreamFailure(t *testing.T) {
	other := map[string]interface{}{}

	AppendStreamStatus(&relaycommon.RelayInfo{IsStream: true}, other)

	streamInfo, ok := other["stream_status"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "error", streamInfo["status"])
	assert.Equal(t, "pre_stream", streamInfo["phase"])
	assert.Equal(t, "not_started", streamInfo["end_reason"])
	assert.Equal(t, 0, streamInfo["received_event_count"])
	assert.Equal(t, false, streamInfo["terminal_seen"])
}
