package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdminUpdateUserSubscriptionRestoresExpiredSubscription verifies validity, quota, reset, and group restoration.
func TestAdminUpdateUserSubscriptionRestoresExpiredSubscription(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	user := &User{Id: 9701, Username: "restore_sub", Status: common.UserStatusEnabled, Group: "default"}
	plan := &SubscriptionPlan{
		Id: 9702, Title: "Pro", Currency: "USD", DurationUnit: SubscriptionDurationMonth,
		DurationValue: 1, QuotaResetPeriod: SubscriptionResetDaily,
	}
	sub := &UserSubscription{
		Id: 9703, UserId: user.Id, PlanId: plan.Id, Status: "expired", StartTime: now - 7200,
		EndTime: now - 3600, AmountTotal: 1000, AmountUsed: 800, UpgradeGroup: "vip",
		PrevUserGroup: "legacy", NextResetTime: 0,
	}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, DB.Create(plan).Error)
	require.NoError(t, DB.Create(sub).Error)

	endTime := now + 48*3600
	amountUsed := int64(250)
	amountTotal := int64(2000)
	result, err := AdminUpdateUserSubscription(sub.Id, UserSubscriptionUpdate{
		EndTime: &endTime, AmountUsed: &amountUsed, AmountTotal: &amountTotal,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "expired", result.Before.Status)
	assert.Equal(t, "active", result.Subscription.Status)
	assert.Equal(t, amountUsed, result.Subscription.AmountUsed)
	assert.Equal(t, amountTotal, result.Subscription.AmountTotal)
	assert.Equal(t, "default", result.Subscription.PrevUserGroup)
	assert.Equal(t, "vip", result.GroupChanged)
	assert.Greater(t, result.Subscription.NextResetTime, now)
	assert.LessOrEqual(t, result.Subscription.NextResetTime, endTime)

	group, err := GetUserGroup(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "vip", group)
}

// TestAdminUpdateUserSubscriptionExpiresImmediately verifies an end-time reduction applies the downgrade in the same request.
func TestAdminUpdateUserSubscriptionExpiresImmediately(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	user := &User{Id: 9711, Username: "expire_sub", Status: common.UserStatusEnabled, Group: "vip"}
	plan := &SubscriptionPlan{Id: 9712, Title: "Pro", Currency: "USD", DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
	sub := &UserSubscription{
		Id: 9713, UserId: user.Id, PlanId: plan.Id, Status: "active", StartTime: now - 3600,
		EndTime: now + 3600, AmountTotal: 1000, AmountUsed: 100, UpgradeGroup: "vip", PrevUserGroup: "default",
	}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, DB.Create(plan).Error)
	require.NoError(t, DB.Create(sub).Error)

	endTime := now - 1
	result, err := AdminUpdateUserSubscription(sub.Id, UserSubscriptionUpdate{EndTime: &endTime})

	require.NoError(t, err)
	assert.Equal(t, "expired", result.Subscription.Status)
	assert.Equal(t, "default", result.GroupChanged)
	group, err := GetUserGroup(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, "default", group)
}

// TestAdminUpdateUserSubscriptionKeepsCancelledStatus verifies editing cannot implicitly restore a cancelled subscription.
func TestAdminUpdateUserSubscriptionKeepsCancelledStatus(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	plan := &SubscriptionPlan{Id: 9721, Title: "Pro", Currency: "USD", DurationUnit: SubscriptionDurationMonth, DurationValue: 1}
	sub := &UserSubscription{
		Id: 9722, UserId: 9723, PlanId: plan.Id, Status: "cancelled", StartTime: now - 3600,
		EndTime: now - 1, AmountTotal: 1000, AmountUsed: 100,
	}
	require.NoError(t, DB.Create(plan).Error)
	require.NoError(t, DB.Create(sub).Error)

	endTime := now + 3600
	result, err := AdminUpdateUserSubscription(sub.Id, UserSubscriptionUpdate{EndTime: &endTime})

	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.Subscription.Status)
	assert.Zero(t, result.Subscription.NextResetTime)
}

// TestAdminUpdateUserSubscriptionRejectsUsedAboveTotal verifies invalid quota edits are fully rolled back.
func TestAdminUpdateUserSubscriptionRejectsUsedAboveTotal(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	sub := &UserSubscription{
		Id: 9731, UserId: 9732, PlanId: 9733, Status: "active", StartTime: now - 3600,
		EndTime: now + 3600, AmountTotal: 1000, AmountUsed: 400,
	}
	require.NoError(t, DB.Create(sub).Error)

	amountTotal := int64(399)
	result, err := AdminUpdateUserSubscription(sub.Id, UserSubscriptionUpdate{AmountTotal: &amountTotal})

	require.ErrorContains(t, err, "已用额度不能超过总额度")
	assert.Nil(t, result)
	var stored UserSubscription
	require.NoError(t, DB.First(&stored, sub.Id).Error)
	assert.EqualValues(t, 1000, stored.AmountTotal)
	assert.EqualValues(t, 400, stored.AmountUsed)
}
