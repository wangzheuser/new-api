package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestUserGroupGrantLifecycle verifies manual grants and subscription snapshots form one active union.
func TestUserGroupGrantLifecycle(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	user := &User{Id: 9801, Username: "grant_user", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, ReplaceUserGroupGrants(user.Id, []UserGroupGrant{
		{GroupName: "manual-permanent", ExpiresAt: 0},
		{GroupName: "manual-active", ExpiresAt: now + 3600},
		{GroupName: "manual-expired", ExpiresAt: now - 1},
	}))
	require.NoError(t, DB.Create(&UserSubscription{
		Id:               9802,
		UserId:           user.Id,
		PlanId:           1,
		Status:           "active",
		StartTime:        now - 60,
		EndTime:          now + 3600,
		EntitlementGroup: "legacy-subscription",
		GrantGroups:      GroupNames{"subscription-b", " subscription-a ", "subscription-a"},
	}).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		Id:          9803,
		UserId:      user.Id,
		PlanId:      2,
		Status:      "cancelled",
		StartTime:   now - 60,
		EndTime:     now + 3600,
		GrantGroups: GroupNames{"cancelled-subscription"},
	}).Error)

	groups, err := GetActiveUserGrantedGroups(user.Id)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"legacy-subscription",
		"manual-active",
		"manual-permanent",
		"subscription-a",
		"subscription-b",
	}, groups)

	require.NoError(t, ReplaceUserGroupGrants(user.Id, []UserGroupGrant{{GroupName: "manual-replaced", ExpiresAt: 0}}))
	manualGrants, err := GetUserGroupGrants(user.Id)
	require.NoError(t, err)
	require.Len(t, manualGrants, 1)
	assert.Equal(t, "manual-replaced", manualGrants[0].GroupName)
}

// TestSubscriptionGrantGroupsSnapshotAndMerge verifies purchase snapshots and merge modes preserve group access.
func TestSubscriptionGrantGroupsSnapshotAndMerge(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 9811, Username: "grant_subscription_user", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Id:                 9812,
		Title:              "Grant plan",
		Currency:           "USD",
		DurationUnit:       SubscriptionDurationHour,
		DurationValue:      1,
		RepeatPurchaseMode: SubscriptionRepeatPurchaseExtendTime,
		GrantGroups:        GroupNames{"group-b", " group-a ", "group-a"},
	}

	var first *SubscriptionApplyResult
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = CreateUserSubscriptionFromPlanTx(tx, user.Id, plan, SubscriptionApplyOptions{Source: "admin"})
		return err
	}))
	require.NotNil(t, first)
	assert.Equal(t, GroupNames{"group-a", "group-b"}, first.Subscription.GrantGroups)

	plan.GrantGroups = GroupNames{"group-c"}
	var second *SubscriptionApplyResult
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		second, err = CreateUserSubscriptionFromPlanTx(tx, user.Id, plan, SubscriptionApplyOptions{Source: "admin"})
		return err
	}))
	require.NotNil(t, second)
	assert.Equal(t, SubscriptionApplyActionMerged, second.Action)
	assert.Equal(t, GroupNames{"group-a", "group-b", "group-c"}, second.Subscription.GrantGroups)
}

// TestSubscriptionUpgradeConflictAndRequestReconciliation verifies the single-upgrade invariant and lazy expiry.
func TestSubscriptionUpgradeConflictAndRequestReconciliation(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 9821, Username: "upgrade_user", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	vipPlan := &SubscriptionPlan{
		Id: 9822, Title: "VIP", Currency: "USD", DurationUnit: SubscriptionDurationHour, DurationValue: 1,
		RepeatPurchaseMode: SubscriptionRepeatPurchaseIndependent, UpgradeGroup: "vip",
	}
	svipPlan := &SubscriptionPlan{
		Id: 9823, Title: "SVIP", Currency: "USD", DurationUnit: SubscriptionDurationHour, DurationValue: 1,
		RepeatPurchaseMode: SubscriptionRepeatPurchaseIndependent, UpgradeGroup: "svip",
	}

	var vipResult *SubscriptionApplyResult
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		vipResult, err = CreateUserSubscriptionFromPlanTx(tx, user.Id, vipPlan, SubscriptionApplyOptions{Source: "admin"})
		return err
	}))
	require.NotNil(t, vipResult)
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := CreateUserSubscriptionFromPlanTx(tx, user.Id, svipPlan, SubscriptionApplyOptions{Source: "admin"})
		return err
	})
	require.ErrorContains(t, err, "基础分组升级 vip")

	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", vipResult.Subscription.Id).
		Updates(map[string]interface{}{"end_time": GetDBTimestamp() - 1, "status": "active"}).Error)
	require.NoError(t, ReconcileDueUserSubscriptions(user.Id))
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "default", group)
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, vipResult.Subscription.Id).Error)
	assert.Equal(t, "expired", subscription.Status)
}

// TestSubscriptionGrantGroupsFundEveryGrantedScope verifies multi-group snapshots share subscription quota.
func TestSubscriptionGrantGroupsFundEveryGrantedScope(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	user := &User{Id: 9831, Username: "grant_scope_user", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	allowOverflow := false
	plan := &SubscriptionPlan{
		Id: 9832, Title: "Multi group", Currency: "USD", DurationUnit: SubscriptionDurationHour, DurationValue: 1,
		Enabled: true, TotalAmount: 100, GrantGroups: GroupNames{"group-a", "group-b"}, AllowWalletOverflow: &allowOverflow,
	}
	require.NoError(t, DB.Create(plan).Error)
	subscription := &UserSubscription{
		Id: 9833, UserId: user.Id, PlanId: plan.Id, Status: "active", StartTime: now - 60, EndTime: now + 3600,
		AmountTotal: 100, GrantGroups: GroupNames{"group-a", "group-b"}, AllowWalletOverflow: false,
	}
	require.NoError(t, DB.Create(subscription).Error)

	for _, group := range []string{"", "group-a", "group-b"} {
		active, err := HasActiveUserSubscriptionForGroup(user.Id, group)
		require.NoError(t, err)
		assert.True(t, active, group)
	}
	active, err := HasActiveUserSubscriptionForGroup(user.Id, "group-c")
	require.NoError(t, err)
	assert.False(t, active)

	result, err := PreConsumeUserSubscriptionForGroup("grant-scope-request", user.Id, "model", 0, 10, "group-b")
	require.NoError(t, err)
	assert.Equal(t, subscription.Id, result.UserSubscriptionId)
	allow, err := UserActiveSubscriptionsAllowWalletOverflowForGroup(user.Id, "group-b")
	require.NoError(t, err)
	assert.False(t, allow)
}

// TestOverlappingSameUpgradeRestoresOriginalBaseGroup verifies parallel upgrades share one base snapshot.
func TestOverlappingSameUpgradeRestoresOriginalBaseGroup(t *testing.T) {
	truncateTables(t)
	user := &User{Id: 9841, Username: "parallel_upgrade_user", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Id: 9842, Title: "VIP", Currency: "USD", DurationUnit: SubscriptionDurationHour, DurationValue: 1,
		RepeatPurchaseMode: SubscriptionRepeatPurchaseIndependent, UpgradeGroup: "vip",
	}

	var first, second *SubscriptionApplyResult
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = CreateUserSubscriptionFromPlanTx(tx, user.Id, plan, SubscriptionApplyOptions{Source: "admin"})
		return err
	}))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		var err error
		second, err = CreateUserSubscriptionFromPlanTx(tx, user.Id, plan, SubscriptionApplyOptions{Source: "admin"})
		return err
	}))
	require.NotNil(t, first)
	require.NotNil(t, second)
	assert.Equal(t, "default", first.Subscription.PrevUserGroup)
	assert.Equal(t, "default", second.Subscription.PrevUserGroup)

	now := GetDBTimestamp()
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", first.Subscription.Id).
		Update("end_time", now-1).Error)
	require.NoError(t, ReconcileDueUserSubscriptions(user.Id))
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "vip", group)

	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", second.Subscription.Id).
		Update("end_time", now-1).Error)
	require.NoError(t, ReconcileDueUserSubscriptions(user.Id))
	group, err = GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "default", group)
}

// TestUnrelatedExpiryDoesNotReplayHistoricalDowngrade verifies old transitions are not applied twice.
func TestUnrelatedExpiryDoesNotReplayHistoricalDowngrade(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	user := &User{Id: 9851, Username: "historical_downgrade_user", Status: common.UserStatusEnabled, Group: "manual-current"}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, DB.Create(&[]UserSubscription{
		{Id: 9852, UserId: user.Id, PlanId: 1, Status: "expired", StartTime: now - 7200, EndTime: now - 3600, UpgradeGroup: "vip", DowngradeGroup: "legacy-target"},
		{Id: 9853, UserId: user.Id, PlanId: 2, Status: "active", StartTime: now - 60, EndTime: now - 1, GrantGroups: GroupNames{"group-a"}},
	}).Error)

	require.NoError(t, ReconcileDueUserSubscriptions(user.Id))
	group, err := GetUserGroup(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, "manual-current", group)
}
