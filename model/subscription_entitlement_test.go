package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubscriptionEntitlementGrantDoesNotChangeBaseGroup verifies the complete admin grant lifecycle.
func TestSubscriptionEntitlementGrantDoesNotChangeBaseGroup(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 7101, Username: "entitlement_user", Status: common.UserStatusEnabled, Group: "国模"}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Id:                 7102,
		Title:              "Claude 体验",
		PriceAmount:        0,
		Currency:           "USD",
		DurationUnit:       SubscriptionDurationHour,
		DurationValue:      24,
		Enabled:            true,
		TotalAmount:        200 * int64(common.QuotaPerUnit),
		RepeatPurchaseMode: SubscriptionRepeatPurchaseExtendTimeAddQuota,
		EntitlementGroup:   "claude",
	}
	require.NoError(t, DB.Create(plan).Error)

	result, err := AdminBindSubscription(user.Id, plan.Id, SubscriptionApplyModePlanDefault)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Subscription)
	assert.Equal(t, "claude", result.Subscription.EntitlementGroup)
	assert.Empty(t, result.Subscription.UpgradeGroup)

	baseGroup, err := GetUserGroup(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "国模", baseGroup)

	groups, err := GetActiveUserEntitlementGroups(user.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude"}, groups)

	_, err = AdminInvalidateUserSubscription(result.Subscription.Id)
	require.NoError(t, err)
	groups, err = GetActiveUserEntitlementGroups(user.Id)
	require.NoError(t, err)
	assert.Empty(t, groups)
	baseGroup, err = GetUserGroup(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "国模", baseGroup)
}

// TestSubscriptionPreConsumeIsolatedByEntitlementGroup verifies same-model subscriptions never share quota across groups.
func TestSubscriptionPreConsumeIsolatedByEntitlementGroup(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 7201, Username: "scope_user", Status: common.UserStatusEnabled, Group: "国模"}
	require.NoError(t, DB.Create(user).Error)

	plans := []*SubscriptionPlan{
		{Id: 7202, Title: "通用", Currency: "USD", DurationUnit: SubscriptionDurationHour, DurationValue: 24, Enabled: true, TotalAmount: 100, RepeatPurchaseMode: SubscriptionRepeatPurchaseIndependent},
		{Id: 7203, Title: "Claude", Currency: "USD", DurationUnit: SubscriptionDurationHour, DurationValue: 24, Enabled: true, TotalAmount: 50, RepeatPurchaseMode: SubscriptionRepeatPurchaseIndependent, EntitlementGroup: "claude"},
		{Id: 7204, Title: "国模", Currency: "USD", DurationUnit: SubscriptionDurationHour, DurationValue: 24, Enabled: true, TotalAmount: 80, RepeatPurchaseMode: SubscriptionRepeatPurchaseIndependent, EntitlementGroup: "国模"},
	}
	for _, plan := range plans {
		require.NoError(t, DB.Create(plan).Error)
		applyRepeatPurchase(t, user.Id, plan, SubscriptionApplyModePlanDefault, false, "admin")
	}

	claudeResult, err := PreConsumeUserSubscriptionForGroup("scope-claude", user.Id, "shared-model", 0, 40, "claude")
	require.NoError(t, err)
	guomoResult, err := PreConsumeUserSubscriptionForGroup("scope-guomo", user.Id, "shared-model", 0, 30, "国模")
	require.NoError(t, err)
	legacyResult, err := PreConsumeUserSubscription("scope-legacy", user.Id, "shared-model", 0, 20)
	require.NoError(t, err)

	assertSubscriptionScope := func(subscriptionId int, expectedGroup string, expectedUsed int64) {
		var subscription UserSubscription
		require.NoError(t, DB.First(&subscription, subscriptionId).Error)
		assert.Equal(t, expectedGroup, subscription.EntitlementGroup)
		assert.Equal(t, expectedUsed, subscription.AmountUsed)
	}
	assertSubscriptionScope(claudeResult.UserSubscriptionId, "claude", 40)
	assertSubscriptionScope(guomoResult.UserSubscriptionId, "国模", 30)
	assertSubscriptionScope(legacyResult.UserSubscriptionId, "", 20)

	_, err = PreConsumeUserSubscriptionForGroup("scope-claude-over", user.Id, "shared-model", 0, 20, "claude")
	require.ErrorContains(t, err, "subscription quota insufficient")
	assertSubscriptionScope(claudeResult.UserSubscriptionId, "claude", 40)
	assertSubscriptionScope(guomoResult.UserSubscriptionId, "国模", 30)
}
