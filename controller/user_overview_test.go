package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildAdminUserOverviewClassifiesAndSanitizesSubscriptions verifies the log overview contract.
func TestBuildAdminUserOverviewClassifiesAndSanitizesSubscriptions(t *testing.T) {
	now := int64(1_700_000_000)
	user := &model.User{
		Id:              7,
		Username:        "reader",
		Password:        "secret-password",
		DisplayName:     "Reader",
		Email:           "reader@example.com",
		Role:            1,
		Status:          1,
		Group:           "default",
		Quota:           1000,
		UsedQuota:       250,
		RequestCount:    12,
		Setting:         `{"billing_preference":"wallet_first"}`,
		StripeCustomer:  "cus_private",
		AffCode:         "CODE",
		AffCount:        2,
		AffQuota:        30,
		AffHistoryQuota: 90,
	}
	subscriptions := []model.SubscriptionSummary{
		{Subscription: &model.UserSubscription{Id: 2, PlanId: 10, Status: "active", StartTime: now - 10, EndTime: now + 200, EntitlementGroup: "vip", GrantGroups: model.GroupNames{"extra", "vip"}, AllowWalletOverflow: true}},
		{Subscription: &model.UserSubscription{Id: 1, PlanId: 10, Status: "active", StartTime: now - 20, EndTime: now + 100}},
		{Subscription: &model.UserSubscription{Id: 4, PlanId: 11, Status: "scheduled", StartTime: now + 300, EndTime: now + 600}},
		{Subscription: &model.UserSubscription{Id: 3, PlanId: 11, Status: "scheduled", StartTime: now + 200, EndTime: now + 500}},
		{Subscription: &model.UserSubscription{Id: 5, PlanId: 12, Status: "expired", StartTime: now - 200, EndTime: now - 100}},
		{Subscription: &model.UserSubscription{Id: 6, PlanId: 12, Status: "cancelled", StartTime: now - 10, EndTime: now + 100}},
	}

	overview := buildAdminUserOverview(user, subscriptions, map[int]string{10: "Pro"}, []string{"vip", "default"}, now)

	assert.Equal(t, "wallet_first", overview.BillingPreference)
	assert.Equal(t, []string{"default", "vip"}, overview.EffectiveGroups)
	require.Len(t, overview.ActiveSubscriptions, 2)
	assert.Equal(t, []int{1, 2}, []int{overview.ActiveSubscriptions[0].Id, overview.ActiveSubscriptions[1].Id})
	assert.Equal(t, "Pro", overview.ActiveSubscriptions[1].PlanTitle)
	assert.Equal(t, model.GroupNames{"extra", "vip"}, overview.ActiveSubscriptions[1].BenefitGroups)
	require.Len(t, overview.ScheduledSubscriptions, 2)
	assert.Equal(t, []int{3, 4}, []int{overview.ScheduledSubscriptions[0].Id, overview.ScheduledSubscriptions[1].Id})

	encoded, err := common.Marshal(overview)
	require.NoError(t, err)
	response := string(encoded)
	assert.NotContains(t, response, "secret-password")
	assert.NotContains(t, response, "cus_private")
	assert.NotContains(t, response, "setting")
	assert.NotContains(t, response, "github_id")
}

// TestBuildAdminUserOverviewReturnsStableDefaults verifies empty collections and billing preference normalization.
func TestBuildAdminUserOverviewReturnsStableDefaults(t *testing.T) {
	overview := buildAdminUserOverview(&model.User{Id: 1}, nil, nil, nil, 100)

	assert.Equal(t, "subscription_first", overview.BillingPreference)
	assert.Empty(t, overview.EffectiveGroups)
	assert.NotNil(t, overview.EffectiveGroups)
	assert.NotNil(t, overview.ActiveSubscriptions)
	assert.NotNil(t, overview.ScheduledSubscriptions)
}
