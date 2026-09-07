package controller

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLegacyClaudeHeaderOnlyRetry protects the narrow HTTP-error retry exception.
func TestLegacyClaudeHeaderOnlyRetry(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, scenario := range []string{"headers", "ping", "event", "sent event", "payload", "error frame", "cancelled", "ping failed", "no budget", "specific channel", "affinity", "local error", "skip retry", "other provider", "responses", "http success"} {
			t.Run(string(format)+"/"+scenario, func(t *testing.T) {
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				helper.SetEventStreamHeaders(ctx)
				require.NoError(t, helper.FlushWriter(ctx))
				status := relaycommon.NewStreamStatus()
				status.MarkAppHTTPCommitted()
				info := &relaycommon.RelayInfo{
					IsStream: true, RelayFormat: format, StreamStatus: status,
					ChannelMeta:      &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
					ChannelRoutePlan: &types.ChannelRoutePlan{RouteMode: types.ChannelRouteModeLegacy},
				}
				err := types.NewErrorWithStatusCode(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, 500, types.ErrOptionWithUpstreamStatusCode(500))
				budget := 2
				switch scenario {
				case "ping":
					require.NoError(t, helper.PingData(ctx))
				case "event":
					info.ReceivedResponseCount = 1
				case "sent event":
					info.SendResponseCount = 1
				case "payload":
					status.MarkClientPayloadCommitted()
				case "error frame":
					status.TryMarkErrorFrameWritten()
				case "cancelled":
					cancelCtx, cancel := context.WithCancel(ctx.Request.Context())
					ctx.Request = ctx.Request.WithContext(cancelCtx)
					cancel()
				case "ping failed":
					status.SetEndReason(relaycommon.StreamEndReasonPingFail, errors.New("closed"))
				case "no budget":
					budget = 0
				case "specific channel":
					ctx.Set("specific_channel_id", 43)
				case "affinity":
					ctx.Set("channel_affinity_skip_retry_on_failure", true)
				case "local error":
					err = types.NewErrorWithStatusCode(errors.New("not implemented"), types.ErrorCodeConvertRequestFailed, 500)
				case "skip retry":
					types.ErrOptionWithSkipRetry()(err)
				case "other provider":
					info.ChannelType = constant.ChannelTypeOpenAI
				case "responses":
					info.RelayFormat = types.RelayFormatOpenAIResponses
				case "http success":
					types.ErrOptionWithUpstreamStatusCode(200)(err)
				}
				assert.Equal(t, scenario == "headers" || scenario == "ping", shouldRetry(ctx, info, err, budget))
			})
		}
	}
}
