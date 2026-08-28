package helper

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamStatusErrorKeepsRetryBeforeBusinessPayload(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Writer.WriteHeader(http.StatusOK)
	status := relaycommon.NewStreamStatus()
	status.MarkAppHTTPCommitted()
	status.SetStreamPolicyVersion("progressive-v1")
	status.SetEndReason(relaycommon.StreamEndReasonUnexpectedEOF, nil)
	info := &relaycommon.RelayInfo{StreamStatus: status}

	relayErr := StreamStatusError(ctx, info)

	require.NotNil(t, relayErr)
	assert.False(t, types.IsSkipRetryError(relayErr), "HTTP headers and Ping are not business payload")
}

func TestStreamStatusErrorKeepsRetryForPlannedRouteBeforeBusinessPayload(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Writer.WriteHeader(http.StatusOK)
	status := relaycommon.NewStreamStatus()
	status.MarkAppHTTPCommitted()
	status.SetRetryCommitPolicy(relaycommon.StreamRetryCommitPolicyPayload)
	status.SetEndReason(relaycommon.StreamEndReasonUnexpectedEOF, nil)
	info := &relaycommon.RelayInfo{StreamStatus: status}

	relayErr := StreamStatusError(ctx, info)

	require.NotNil(t, relayErr)
	assert.False(t, types.IsSkipRetryError(relayErr), "planned routes retry until a business event is committed")
}

func TestStreamStatusErrorStopsRetryAfterBusinessPayload(t *testing.T) {
	t.Parallel()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	status := relaycommon.NewStreamStatus()
	status.SetStreamPolicyVersion("progressive-v1")
	status.MarkClientPayloadCommitted()
	status.SetEndReason(relaycommon.StreamEndReasonUnexpectedEOF, nil)
	info := &relaycommon.RelayInfo{StreamStatus: status}

	relayErr := StreamStatusError(ctx, info)

	require.NotNil(t, relayErr)
	assert.True(t, types.IsSkipRetryError(relayErr))
}

func TestStreamStatusErrorStopsRetryWhenDownstreamPingFails(t *testing.T) {
	t.Parallel()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	status := relaycommon.NewStreamStatus()
	status.SetRetryCommitPolicy(relaycommon.StreamRetryCommitPolicyPayload)
	status.SetEndReason(relaycommon.StreamEndReasonPingFail, errors.New("client write failed"))
	info := &relaycommon.RelayInfo{StreamStatus: status}

	relayErr := StreamStatusError(ctx, info)

	require.NotNil(t, relayErr)
	assert.True(t, types.IsSkipRetryError(relayErr))
}
