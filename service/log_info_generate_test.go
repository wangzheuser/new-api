package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateTextOtherInfoIncludesResponseOverrideAudit verifies the complete response decision remains admin-only and serializable.
func TestGenerateTextOtherInfoIncludesResponseOverrideAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("use_channel", []string{"17"})
	startTime := time.Unix(1_700_000_000, 0)
	semantics := relaycommon.ResponseSemantics{
		Response: relaycommon.ResponseSemanticSummary{
			TransportStatus: relaycommon.ResponseTransportSuccess,
			PrimaryOutcome:  relaycommon.ResponseOutcomeRejected,
			RejectionState:  relaycommon.ResponseRejectionAll,
			OutputState:     relaycommon.ResponseOutputEmpty,
			UsageState:      relaycommon.ResponseUsageUpstream,
			StreamState:     relaycommon.ResponseStreamNotStreamed,
			Items: []relaycommon.ResponseSemanticItem{{
				Index:            0,
				PrimaryOutcome:   relaycommon.ResponseOutcomeRejected,
				RejectionState:   relaycommon.ResponseRejectionAll,
				OutputState:      relaycommon.ResponseOutputEmpty,
				ProviderReason:   "content_filter",
				NormalizedReason: "content_filter",
			}},
		},
		Upstream: relaycommon.ResponseEndpointSemantics{Format: types.RelayFormatOpenAI, HTTPStatus: http.StatusOK},
		Client:   relaycommon.ResponseEndpointSemantics{Format: types.RelayFormatOpenAI, HTTPStatus: http.StatusForbidden},
	}
	decision := &relaycommon.ResponseOverrideDecision{
		Configured:           true,
		Evaluated:            true,
		Applied:              true,
		RuleID:               "operation[2]",
		RuleIndex:            2,
		Description:          "Map an upstream business rejection to a client error",
		UpstreamStatusCode:   http.StatusOK,
		ClientStatusCode:     http.StatusForbidden,
		Semantics:            semantics,
		Billable:             true,
		Retryable:            false,
		AffectsChannelHealth: false,
	}
	info := &relaycommon.RelayInfo{
		StartTime:         startTime,
		FirstResponseTime: startTime.Add(250 * time.Millisecond),
		ResponseOverride:  decision,
		ChannelMeta:       &relaycommon.ChannelMeta{},
	}

	other := GenerateTextOtherInfo(ctx, info, 1, 1, 1, 0, 0, 0, 1)

	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Same(t, decision, adminInfo["response_override"])
	assert.NotContains(t, other, "response_override")

	encoded, err := common.Marshal(adminInfo["response_override"])
	require.NoError(t, err)
	var audit map[string]interface{}
	require.NoError(t, common.Unmarshal(encoded, &audit))
	assert.Equal(t, true, audit["applied"])
	assert.Equal(t, "operation[2]", audit["rule_id"])
	assert.Equal(t, float64(2), audit["rule_index"])
	assert.Equal(t, "Map an upstream business rejection to a client error", audit["description"])
	assert.Equal(t, float64(http.StatusOK), audit["upstream_status_code"])
	assert.Equal(t, float64(http.StatusForbidden), audit["client_status_code"])
	assert.Equal(t, true, audit["billable"])
	assert.Equal(t, false, audit["retryable"])
	assert.Equal(t, false, audit["affects_channel_health"])

	serializedSemantics, ok := audit["semantics"].(map[string]interface{})
	require.True(t, ok)
	response, ok := serializedSemantics["response"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, relaycommon.ResponseTransportSuccess, response["transport_status"])
	assert.Equal(t, relaycommon.ResponseOutcomeRejected, response["primary_outcome"])
	assert.Equal(t, relaycommon.ResponseRejectionAll, response["rejection_state"])
	assert.Equal(t, relaycommon.ResponseUsageUpstream, response["usage_state"])
	upstream, ok := serializedSemantics["upstream"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(http.StatusOK), upstream["http_status"])
	client, ok := serializedSemantics["client"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(http.StatusForbidden), client["http_status"])
}

func TestAppendStreamStatusIncludesProtocolTerminalAndTransportEnd(t *testing.T) {
	streamStatus := relaycommon.NewStreamStatus()
	streamStatus.SetTerminal("response.failed", "failed")
	streamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, errors.New("mid stream aborted"))
	streamStatus.RecordError("malformed event")
	streamStatus.ObserveToolPayloadBytes(8, 21)
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
	assert.Equal(t, int64(8), streamInfo["partial_tool_name_bytes"])
	assert.Equal(t, int64(21), streamInfo["partial_tool_argument_bytes"])
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
