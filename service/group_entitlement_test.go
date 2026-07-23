package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetUserEffectiveGroupsIncludesOnlyActiveEntitlements verifies group access follows subscription validity.
func TestGetUserEffectiveGroupsIncludesOnlyActiveEntitlements(t *testing.T) {
	truncate(t)
	now := time.Now().Unix()
	require.NoError(t, model.DB.Create(&model.UserSubscription{
		Id: 7301, UserId: 7302, PlanId: 1, Status: "active", StartTime: now - 60, EndTime: now + 3600, EntitlementGroup: "claude",
	}).Error)
	require.NoError(t, model.DB.Create(&model.UserSubscription{
		Id: 7303, UserId: 7302, PlanId: 2, Status: "active", StartTime: now - 3600, EndTime: now - 1, EntitlementGroup: "expired-group",
	}).Error)

	groups, err := GetUserEffectiveGroups(7302, "国模")
	require.NoError(t, err)
	assert.Contains(t, groups, "国模")
	assert.Contains(t, groups, "claude")
	assert.NotContains(t, groups, "expired-group")
}

// TestGroupInUserEffectiveGroupsRejectsCancelledEntitlement verifies cancellation removes the last group grant.
func TestGroupInUserEffectiveGroupsRejectsCancelledEntitlement(t *testing.T) {
	truncate(t)
	previousGroups := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":""}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
	})
	now := time.Now().Unix()
	require.NoError(t, model.DB.Create(&model.UserSubscription{
		Id:               7310,
		UserId:           7311,
		PlanId:           1,
		Status:           "cancelled",
		StartTime:        now - 60,
		EndTime:          now,
		EntitlementGroup: "国模",
	}).Error)

	allowed, err := GroupInUserEffectiveGroups(7311, "default", "国模")
	require.NoError(t, err)
	assert.False(t, allowed)
}
