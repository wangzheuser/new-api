package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetCurrentAndScheduledUserSubscriptionsFiltersEndedHistory verifies the overview query boundary.
func TestGetCurrentAndScheduledUserSubscriptionsFiltersEndedHistory(t *testing.T) {
	truncateTables(t)
	now := int64(1_700_000_000)
	require.NoError(t, DB.Create(&[]UserSubscription{
		{UserId: 41, PlanId: 1, Status: "active", StartTime: now - 10, EndTime: now + 100},
		{UserId: 41, PlanId: 2, Status: "scheduled", StartTime: now + 100, EndTime: now + 200},
		{UserId: 41, PlanId: 3, Status: "expired", StartTime: now - 200, EndTime: now - 100},
		{UserId: 41, PlanId: 4, Status: "cancelled", StartTime: now - 10, EndTime: now + 100},
		{UserId: 42, PlanId: 5, Status: "active", StartTime: now - 10, EndTime: now + 100},
	}).Error)

	subscriptions, err := GetCurrentAndScheduledUserSubscriptions(41, now)
	require.NoError(t, err)
	require.Len(t, subscriptions, 2)
	planIds := []int{subscriptions[0].Subscription.PlanId, subscriptions[1].Subscription.PlanId}
	assert.ElementsMatch(t, []int{1, 2}, planIds)
}

// TestGetSubscriptionPlanTitlesReturnsExistingPlans verifies title lookup remains tolerant of missing plans.
func TestGetSubscriptionPlanTitlesReturnsExistingPlans(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&[]SubscriptionPlan{
		{Id: 11, Title: "Pro"},
		{Id: 12, Title: "Team"},
	}).Error)

	titles, err := GetSubscriptionPlanTitles([]int{11, 12, 999})
	require.NoError(t, err)
	assert.Equal(t, map[int]string{11: "Pro", 12: "Team"}, titles)
}
