package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
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
