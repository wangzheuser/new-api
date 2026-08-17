package service

import (
	"errors"
	"net/http"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type textBillingFinalizerSpy struct {
	refundCalls int
}

// Settle records no state because this spy only verifies the refund branch.
func (s *textBillingFinalizerSpy) Settle(int) error { return nil }

// Refund records how many financial applications reached the billing session.
func (s *textBillingFinalizerSpy) Refund(*gin.Context) { s.refundCalls++ }

// NeedsRefund reports an unsettled pre-consume for the refund fixture.
func (s *textBillingFinalizerSpy) NeedsRefund() bool { return true }

// GetPreConsumedQuota returns the fixture pre-consume amount.
func (s *textBillingFinalizerSpy) GetPreConsumedQuota() int { return 10 }

// Reserve is unused by the finalizer refund branch.
func (s *textBillingFinalizerSpy) Reserve(int) error { return nil }

func TestTextBillingFinalizationUsesPartialSettleOnlyAfterPayloadCommit(t *testing.T) {
	t.Parallel()
	relayErr := types.NewOpenAIError(errors.New("stream failed"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	info := &relaycommon.RelayInfo{IsStream: true, StreamStatus: relaycommon.NewStreamStatus()}
	info.StreamStatus.SetStreamPolicyVersion("progressive-v1")

	assert.Equal(t, relaycommon.BillingRefunded, textBillingFinalization(info, relayErr))
	info.StreamStatus.MarkAppHTTPCommitted()
	assert.Equal(t, relaycommon.BillingRefunded, textBillingFinalization(info, relayErr))
	info.StreamStatus.MarkClientPayloadCommitted()
	assert.Equal(t, relaycommon.BillingSettledPartial, textBillingFinalization(info, relayErr))
	assert.Equal(t, relaycommon.BillingSettled, textBillingFinalization(info, nil))
}

func TestFinalizeTextBillingRefundApplicationIsIdempotent(t *testing.T) {
	t.Parallel()
	relayErr := types.NewOpenAIError(errors.New("precommit failed"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	spy := &textBillingFinalizerSpy{}
	info := &relaycommon.RelayInfo{
		IsStream:     true,
		StreamStatus: relaycommon.NewStreamStatus(),
		Billing:      spy,
	}
	info.StreamStatus.SetStreamPolicyVersion("progressive-v1")
	ctx, _ := gin.CreateTestContext(nil)

	first, firstErr := FinalizeTextBilling(ctx, info, nil, relayErr)
	second, secondErr := FinalizeTextBilling(ctx, info, nil, relayErr)

	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	assert.Equal(t, relaycommon.BillingRefunded, first)
	assert.Equal(t, relaycommon.BillingRefunded, second)
	assert.Equal(t, 1, spy.refundCalls)
}
