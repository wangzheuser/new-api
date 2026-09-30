package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBillingSessionRefundsPreConsumeAfterStreamFailure 验证钱包及订阅退款同时恢复令牌且不会重复入账。
func TestBillingSessionRefundsPreConsumeAfterStreamFailure(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			truncate(t)
			seedUser(t, 9451, 900)
			seedToken(t, 9452, 9451, "stream-refund-token", 900)
			require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 9452).Update("used_quota", 100).Error)
			var funding FundingSource = &WalletFunding{userId: 9451}
			if source == BillingSourceSubscription {
				seedSubscription(t, 9453, 9451, 1000, 100)
				require.NoError(t, model.DB.Create(&model.SubscriptionPreConsumeRecord{
					RequestId: "stream-refund", UserId: 9451, UserSubscriptionId: 9453, PreConsumed: 100, Status: "consumed",
				}).Error)
				funding = &SubscriptionFunding{requestId: "stream-refund", subscriptionId: 9453, preConsumed: 100}
			}
			session := &BillingSession{
				relayInfo: &relaycommon.RelayInfo{UserId: 9451, TokenId: 9452, TokenKey: "stream-refund-token"},
				funding:   funding, preConsumedQuota: 100, tokenConsumed: 100,
			}
			ctx := newEntitlementBillingContext()
			require.True(t, session.NeedsRefund())
			session.Refund(ctx)
			session.Refund(ctx)
			var token model.Token
			require.Eventually(t, func() bool {
				return model.DB.First(&token, 9452).Error == nil && token.RemainQuota == 1000
			}, 2*time.Second, time.Millisecond)
			assert.Zero(t, token.UsedQuota)
			assert.False(t, session.NeedsRefund())
			if source == BillingSourceWallet {
				assert.Equal(t, 1000, getUserQuota(t, 9451))
			} else {
				var sub model.UserSubscription
				require.NoError(t, model.DB.First(&sub, 9453).Error)
				assert.Zero(t, sub.AmountUsed)
			}
		})
	}
}
