package service

import (
	"errors"
	"math"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestBillingSettlementAtomicity 在真实事务中注入资金或令牌写失败，验证没有半笔扣费。
func TestBillingSettlementAtomicity(t *testing.T) {
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		for _, failingTable := range []string{"", "tokens", "funding"} {
			t.Run(source+"/"+failingTable, func(t *testing.T) {
				truncate(t)
				seedUser(t, 9201, 900)
				seedToken(t, 9202, 9201, "atomic-billing-token", 900)
				require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 9202).Update("used_quota", 100).Error)
				info := &relaycommon.RelayInfo{UserId: 9201, TokenId: 9202, TokenKey: "atomic-billing-token", BillingSource: source, FinalPreConsumedQuota: 100, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}}
				var funding FundingSource = &WalletFunding{userId: 9201}
				fundingTable := "users"
				if source == BillingSourceSubscription {
					seedSubscription(t, 9203, 9201, 1000, 100)
					info.SubscriptionId = 9203
					funding = &SubscriptionFunding{subscriptionId: 9203, preConsumed: 100}
					fundingTable = "user_subscriptions"
				}
				session := &BillingSession{relayInfo: info, funding: funding, preConsumedQuota: 100, tokenConsumed: 100}
				info.Billing = session
				injected := errors.New("injected billing write failure")
				if failingTable != "" {
					table := failingTable
					if table == "funding" {
						table = fundingTable
					}
					require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:billing_failure", func(tx *gorm.DB) {
						if tx.Statement.Table == table {
							tx.AddError(injected)
						}
					}))
					t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove("test:billing_failure")) })
				}
				err := SettleBilling(newEntitlementBillingContext(), info, 180)
				if failingTable == "" {
					require.NoError(t, err)
					assert.Equal(t, "settled", info.BillingSettlementState)
				} else {
					require.ErrorIs(t, err, injected)
					assert.Equal(t, "failed", info.BillingSettlementState)
					// 修复底层故障也不盲目重放结果可能不确定的提交。
					require.NoError(t, model.DB.Callback().Update().Remove("test:billing_failure"))
				}
				assert.Equal(t, err, session.Settle(180))
				assert.Error(t, session.Settle(181))
				assert.Error(t, session.Reserve(200))
				assert.False(t, session.NeedsRefund())
				session.Refund(newEntitlementBillingContext())
				wantQuota, wantUsed := 820, int64(180)
				if failingTable != "" {
					wantQuota, wantUsed = 900, 100
				}
				// 持久化的任务保留应收金额，后续退款只使用实际入账额度。
				task := model.InitTask("", info)
				require.NoError(t, task.Insert())
				var savedTask model.Task
				require.NoError(t, model.DB.First(&savedTask, task.ID).Error)
				assert.EqualValues(t, wantUsed, savedTask.Quota)
				assert.Equal(t, info.BillingSettlementState, savedTask.PrivateData.BillingSettlementState)
				assert.Equal(t, info.BillingSettlementError, savedTask.PrivateData.BillingSettlementError)
				assert.Equal(t, 180, savedTask.PrivateData.BillingSettlementQuota)
				var token model.Token
				require.NoError(t, model.DB.First(&token, 9202).Error)
				assert.Equal(t, wantQuota, token.RemainQuota)
				assert.EqualValues(t, 1000-wantQuota, token.UsedQuota)
				if source == BillingSourceWallet {
					quota, err := model.GetUserQuota(9201, true)
					require.NoError(t, err)
					assert.Equal(t, wantQuota, quota)
				} else {
					var sub model.UserSubscription
					require.NoError(t, model.DB.First(&sub, 9203).Error)
					assert.Equal(t, wantUsed, sub.AmountUsed)
				}
			})
		}
	}
}

// TestBillingSettlementQuotaLimits 验证资金余额、有限令牌余额和无限令牌用量边界。
func TestBillingSettlementQuotaLimits(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		wallet, remain, used int
		unlimited, wantError bool
	}{
		{name: "wallet_insufficient", wallet: 0, remain: 100, used: 100, wantError: true},
		{name: "limited_token_insufficient", wallet: 100, remain: 0, used: 100, wantError: true},
		{name: "unlimited_token_zero", wallet: 100, remain: 0, used: 100, unlimited: true},
		{name: "unlimited_token_negative", wallet: 100, remain: -100, used: 100, unlimited: true},
		{name: "unlimited_token_usage_overflow", wallet: 100, remain: 0, used: math.MaxInt32, unlimited: true, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			truncate(t)
			seedUser(t, 9251, tt.wallet)
			seedToken(t, 9252, 9251, "overdraft-token", tt.remain)
			require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 9252).Updates(map[string]interface{}{
				"used_quota": tt.used, "unlimited_quota": tt.unlimited,
			}).Error)
			info := &relaycommon.RelayInfo{UserId: 9251, TokenId: 9252, TokenKey: "overdraft-token", TokenUnlimited: tt.unlimited}
			info.Billing = &BillingSession{relayInfo: info, funding: &WalletFunding{userId: 9251}, preConsumedQuota: 100}
			err := SettleBilling(newEntitlementBillingContext(), info, 150)
			delta := 50
			if tt.wantError {
				require.Error(t, err)
				assert.Equal(t, "failed", info.BillingSettlementState)
				delta = 0
			} else {
				require.NoError(t, err)
				assert.Equal(t, "settled", info.BillingSettlementState)
			}
			var user model.User
			require.NoError(t, model.DB.First(&user, 9251).Error)
			assert.Equal(t, tt.wallet-delta, user.Quota)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 9252).Error)
			assert.Equal(t, tt.remain-delta, token.RemainQuota)
			assert.Equal(t, tt.used+delta, token.UsedQuota)
		})
	}
}

// TestSubscriptionReserveClassifiesDatabaseFailure 保留额度不足与数据故障的业务区别。
func TestSubscriptionReserveClassifiesDatabaseFailure(t *testing.T) {
	truncate(t)
	seedUser(t, 9301, 1000)
	seedSubscription(t, 9303, 9301, 100, 90)
	info := &relaycommon.RelayInfo{UserId: 9301, SubscriptionId: 9303, IsPlayground: true}
	session := &BillingSession{relayInfo: info, funding: &SubscriptionFunding{subscriptionId: 9303}, preConsumedQuota: 90}
	var apiErr *types.NewAPIError
	require.ErrorAs(t, session.Reserve(110), &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:reserve_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "user_subscriptions" {
			tx.AddError(errors.New("database unavailable"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove("test:reserve_failure")) })
	require.ErrorAs(t, session.Reserve(95), &apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeUpdateDataError, apiErr.GetErrorCode())
	session.refunded = true
	assert.Error(t, session.Settle(90))
}

// TestSubscriptionRefundAtomicity 验证订阅预扣记录、订阅用量和令牌退款同事务幂等。
func TestSubscriptionRefundAtomicity(t *testing.T) {
	truncate(t)
	seedUser(t, 9351, 1000)
	seedToken(t, 9352, 9351, "subscription-refund-token", 900)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 9352).Update("used_quota", 120).Error)
	seedSubscription(t, 9353, 9351, 1000, 120)
	record := &model.SubscriptionPreConsumeRecord{RequestId: "subscription-refund-request", UserId: 9351, UserSubscriptionId: 9353, PreConsumed: 100, Status: "consumed"}
	require.NoError(t, model.DB.Create(record).Error)
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:refund_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("token refund failed"))
		}
	}))
	err := model.RefundSubscriptionBilling(record.RequestId, 9351, 9352, "subscription-refund-token", 20)
	require.Error(t, err)
	require.NoError(t, model.DB.Callback().Update().Remove("test:refund_failure"))
	var sub model.UserSubscription
	require.NoError(t, model.DB.First(&sub, 9353).Error)
	assert.EqualValues(t, 120, sub.AmountUsed)
	var pending model.SubscriptionPreConsumeRecord
	require.NoError(t, model.DB.First(&pending, record.Id).Error)
	assert.Equal(t, "consumed", pending.Status)

	require.NoError(t, model.RefundSubscriptionBilling(record.RequestId, 9351, 9352, "subscription-refund-token", 20))
	require.NoError(t, model.RefundSubscriptionBilling(record.RequestId, 9351, 9352, "subscription-refund-token", 20))
	require.NoError(t, model.DB.First(&sub, 9353).Error)
	assert.Zero(t, sub.AmountUsed)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 9352).Error)
	assert.Equal(t, 1020, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
}

// TestFailedStreamSettlementRemainsFailed 验证失败状态、日志及统计不会伪装成结算成功。
func TestFailedStreamSettlementRemainsFailed(t *testing.T) {
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	truncate(t)
	seedUser(t, 9401, 10000)
	seedToken(t, 9402, 9401, "failed-settlement-token", 10000)
	info := inputPolicyBillingInfo()
	info.UserId, info.TokenId, info.TokenKey = 9401, 9402, "failed-settlement-token"
	info.UserSetting = dto.UserSetting{BillingPreference: "wallet_only"}
	info.IsStream, info.StreamStatus = true, relaycommon.NewStreamStatus()
	ctx := newEntitlementBillingContext()
	session, apiErr := NewBillingSession(ctx, info, 100)
	require.Nil(t, apiErr)
	info.Billing = session
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:stream_settlement_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("token write failed"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove("test:stream_settlement_failure")) })
	for range 2 {
		state, err := FinalizeTextBilling(ctx, info, &dto.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}, nil)
		require.Error(t, err)
		assert.Equal(t, relaycommon.BillingFailed, state)
	}
	var user model.User
	require.NoError(t, model.DB.First(&user, 9401).Error)
	assert.Equal(t, 9900, user.Quota)
	assert.Zero(t, user.UsedQuota)
	assert.Zero(t, user.RequestCount)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ?", 9401).Find(&logs).Error)
	require.Len(t, logs, 1)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
	settlement := other["admin_info"].(map[string]interface{})["billing_settlement"].(map[string]interface{})
	assert.Equal(t, "failed", settlement["state"])
	assert.EqualValues(t, 100, settlement["pre_consumed_quota"])
}
