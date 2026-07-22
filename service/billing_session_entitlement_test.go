package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newEntitlementBillingContext creates the request context required by the billing session.
func newEntitlementBillingContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return ctx
}

// seedEntitlementBillingSubscription creates a plan snapshot for one billing scope.
func seedEntitlementBillingSubscription(t *testing.T, id, userId int, group string, amount int64, allowOverflow bool) {
	t.Helper()
	plan := &model.SubscriptionPlan{
		Id: id, Title: group + " plan", Currency: "USD", DurationUnit: model.SubscriptionDurationHour,
		DurationValue: 24, Enabled: true, TotalAmount: amount, EntitlementGroup: group,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	now := time.Now().Unix()
	require.NoError(t, model.DB.Create(&model.UserSubscription{
		Id: id, UserId: userId, PlanId: id, AmountTotal: amount, Status: "active",
		StartTime: now - 60, EndTime: now + 3600, EntitlementGroup: group,
		AllowWalletOverflow: allowOverflow,
	}).Error)
}

// TestNewBillingSessionUsesMatchingEntitlementGroup verifies scoped subscriptions override wallet preference.
func TestNewBillingSessionUsesMatchingEntitlementGroup(t *testing.T) {
	truncate(t)
	seedUser(t, 7401, 1000)
	seedEntitlementBillingSubscription(t, 7402, 7401, "claude", 100, false)
	seedEntitlementBillingSubscription(t, 7403, 7401, "国模", 100, false)
	relayInfo := &relaycommon.RelayInfo{
		RequestId: "billing-claude", UserId: 7401, UsingGroup: "claude", OriginModelName: "shared-model",
		IsPlayground: true, UserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
	}

	session, apiErr := NewBillingSession(newEntitlementBillingContext(), relayInfo, 40)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Equal(t, BillingSourceSubscription, relayInfo.BillingSource)
	assert.Equal(t, "claude", relayInfo.SubscriptionEntitlementGroup)
	assert.Equal(t, 7402, relayInfo.SubscriptionId)

	var claudeSub model.UserSubscription
	var guomoSub model.UserSubscription
	require.NoError(t, model.DB.First(&claudeSub, 7402).Error)
	require.NoError(t, model.DB.First(&guomoSub, 7403).Error)
	assert.EqualValues(t, 40, claudeSub.AmountUsed)
	assert.Zero(t, guomoSub.AmountUsed)
	quota, err := model.GetUserQuota(7401, true)
	require.NoError(t, err)
	assert.Equal(t, 1000, quota)
}

// TestNewBillingSessionScopedOverflowPolicy verifies strict and wallet-overflow group behavior.
func TestNewBillingSessionScopedOverflowPolicy(t *testing.T) {
	truncate(t)
	seedUser(t, 7501, 1000)
	seedEntitlementBillingSubscription(t, 7502, 7501, "claude", 50, false)
	seedEntitlementBillingSubscription(t, 7503, 7501, "国模", 500, true)

	strictInfo := &relaycommon.RelayInfo{
		RequestId: "billing-strict", UserId: 7501, UsingGroup: "claude", OriginModelName: "shared-model",
		IsPlayground: true, UserSetting: dto.UserSetting{BillingPreference: "subscription_first"},
	}
	strictSession, apiErr := NewBillingSession(newEntitlementBillingContext(), strictInfo, 60)
	assert.Nil(t, strictSession)
	require.NotNil(t, apiErr)
	quota, err := model.GetUserQuota(7501, true)
	require.NoError(t, err)
	assert.Equal(t, 1000, quota)
	var otherGroupSub model.UserSubscription
	require.NoError(t, model.DB.First(&otherGroupSub, 7503).Error)
	assert.Zero(t, otherGroupSub.AmountUsed)

	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", 7502).Update("allow_wallet_overflow", true).Error)
	overflowInfo := &relaycommon.RelayInfo{
		RequestId: "billing-overflow", UserId: 7501, UsingGroup: "claude", OriginModelName: "shared-model",
		IsPlayground: true, UserSetting: dto.UserSetting{BillingPreference: "subscription_only"},
	}
	overflowSession, apiErr := NewBillingSession(newEntitlementBillingContext(), overflowInfo, 60)
	require.Nil(t, apiErr)
	require.NotNil(t, overflowSession)
	assert.Equal(t, BillingSourceWallet, overflowInfo.BillingSource)
	quota, err = model.GetUserQuota(7501, true)
	require.NoError(t, err)
	assert.Equal(t, 940, quota)
}

// TestNewBillingSessionLegacySubscriptionIgnoresOtherEntitlementGroups verifies generic billing cannot consume scoped quota.
func TestNewBillingSessionLegacySubscriptionIgnoresOtherEntitlementGroups(t *testing.T) {
	truncate(t)
	seedUser(t, 7601, 1000)
	seedEntitlementBillingSubscription(t, 7602, 7601, "claude", 500, false)
	plan := &model.SubscriptionPlan{
		Id: 7603, Title: "legacy", Currency: "USD", DurationUnit: model.SubscriptionDurationHour,
		DurationValue: 24, Enabled: true, TotalAmount: 100,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	now := time.Now().Unix()
	require.NoError(t, model.DB.Create(&model.UserSubscription{
		Id: 7603, UserId: 7601, PlanId: 7603, AmountTotal: 100, Status: "active",
		StartTime: now - 60, EndTime: now + 3600, AllowWalletOverflow: true,
	}).Error)
	relayInfo := &relaycommon.RelayInfo{
		RequestId: "billing-legacy", UserId: 7601, UsingGroup: "default", OriginModelName: "shared-model",
		IsPlayground: true, UserSetting: dto.UserSetting{BillingPreference: "subscription_first"},
	}

	session, apiErr := NewBillingSession(newEntitlementBillingContext(), relayInfo, 30)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Equal(t, 7603, relayInfo.SubscriptionId)
	assert.Empty(t, relayInfo.SubscriptionEntitlementGroup)
	var scoped model.UserSubscription
	require.NoError(t, model.DB.First(&scoped, 7602).Error)
	assert.Zero(t, scoped.AmountUsed)
}
