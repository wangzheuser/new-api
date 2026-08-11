package service

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type refundTestFunding struct {
	refunded atomic.Int32
	done     chan struct{}
}

// Source returns the wallet funding identifier used by the billing session.
func (f *refundTestFunding) Source() string { return BillingSourceWallet }

// PreConsume is already represented by the session fixture state.
func (f *refundTestFunding) PreConsume(int) error {
	return nil
}

// Settle is unused by the failure-path fixture.
func (f *refundTestFunding) Settle(int) error {
	return nil
}

// Refund records the asynchronous refund invocation.
func (f *refundTestFunding) Refund() error {
	f.refunded.Add(1)
	close(f.done)
	return nil
}

func TestBillingSessionRefundsPreConsumeAfterStreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	funding := &refundTestFunding{done: make(chan struct{})}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 40,
		tokenConsumed:    40,
	}

	require.True(t, session.NeedsRefund())
	session.Refund(c)

	select {
	case <-funding.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for refund")
	}
	assert.Equal(t, int32(1), funding.refunded.Load())
	assert.False(t, session.NeedsRefund())

	// 退款幂等：重复调用不会再次触发资金源退款。
	session.Refund(c)
	assert.Equal(t, int32(1), funding.refunded.Load())
}
