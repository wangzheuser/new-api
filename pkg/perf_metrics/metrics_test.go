package perfmetrics

import (
	"context"
	"io"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 部分流已结算并不表示客户端收到了完整的响应。
func TestPartialSettlementAfterUnexpectedEOFCountsAsFailure(t *testing.T) {
	status := relaycommon.NewStreamStatus()
	status.MarkClientPayloadCommitted()
	status.SetEndReason(relaycommon.StreamEndReasonUnexpectedEOF, io.ErrUnexpectedEOF)
	status.SetTerminal("error", "failed")
	status.SetBillingFinalization(relaycommon.BillingSettledPartial)
	info := &relaycommon.RelayInfo{IsStream: true, StreamStatus: status}
	assert.Equal(t, OutcomeFailure, ClassifyRelayOutcome(context.Background(), info, nil))
}

// TestQuerySummaryAggregation 验证总成功率按请求量加权，并隐藏小样本模型。
func TestQuerySummaryAggregation(t *testing.T) {
	totals := map[string]counters{
		"high-traffic": {requestCount: 1000, successCount: 990},
		"small-sample": {requestCount: 42, successCount: 0},
	}

	result := buildSummaryAllResult(totals, nil)

	require.Len(t, result.Models, 1)
	assert.Equal(t, "high-traffic", result.Models[0].ModelName)
	assert.Equal(t, 95.01, result.SuccessRate)

	detail := buildQueryResult("small-sample", map[bucketKey]counters{
		{model: "small-sample", group: "default", bucketTs: 1}: {requestCount: 42},
	})
	assert.Empty(t, detail.Groups)
}
