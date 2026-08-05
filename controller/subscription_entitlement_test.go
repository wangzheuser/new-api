package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSubscriptionPlanGroups(t *testing.T) {
	valid := &model.SubscriptionPlan{EntitlementGroup: " default "}
	require.NoError(t, normalizeSubscriptionPlanGroups(valid))
	assert.Equal(t, "default", valid.EntitlementGroup)

	conflict := &model.SubscriptionPlan{EntitlementGroup: "default", UpgradeGroup: "default"}
	require.ErrorContains(t, normalizeSubscriptionPlanGroups(conflict), "不能与升级或降级分组同时配置")

	missing := &model.SubscriptionPlan{EntitlementGroup: "missing-entitlement-group"}
	require.ErrorContains(t, normalizeSubscriptionPlanGroups(missing), "权益分组不存在")

	combined := &model.SubscriptionPlan{UpgradeGroup: "vip", GrantGroups: model.GroupNames{" default ", "default"}}
	require.NoError(t, normalizeSubscriptionPlanGroups(combined))
	assert.Equal(t, model.GroupNames{"default"}, combined.GrantGroups)

	duplicateUpgrade := &model.SubscriptionPlan{UpgradeGroup: "vip", GrantGroups: model.GroupNames{"vip"}}
	require.ErrorContains(t, normalizeSubscriptionPlanGroups(duplicateUpgrade), "已自动可用")
}
