package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSubscriptionPlanGroups(t *testing.T) {
	valid := &model.SubscriptionPlan{QuotaScope: model.SubscriptionQuotaGroups, GrantGroups: model.GroupNames{" default "}}
	require.NoError(t, normalizeSubscriptionPlanGroups(valid))
	assert.Equal(t, model.GroupNames{"default"}, valid.GrantGroups)

	conflict := &model.SubscriptionPlan{QuotaScope: model.SubscriptionQuotaGeneral, GrantGroups: model.GroupNames{"default"}}
	require.ErrorContains(t, normalizeSubscriptionPlanGroups(conflict), "额度适用范围与权益分组不匹配")

	missing := &model.SubscriptionPlan{QuotaScope: model.SubscriptionQuotaGroups, GrantGroups: model.GroupNames{"missing-entitlement-group"}}
	require.ErrorContains(t, normalizeSubscriptionPlanGroups(missing), "不存在")

	combined := &model.SubscriptionPlan{UpgradeGroup: "vip", QuotaScope: model.SubscriptionQuotaGroupsAndGeneral, GrantGroups: model.GroupNames{" default ", "default"}}
	require.NoError(t, normalizeSubscriptionPlanGroups(combined))
	assert.Equal(t, model.GroupNames{"default"}, combined.GrantGroups)

	duplicateUpgrade := &model.SubscriptionPlan{UpgradeGroup: "vip", QuotaScope: model.SubscriptionQuotaGroupsAndGeneral, GrantGroups: model.GroupNames{"vip"}}
	require.ErrorContains(t, normalizeSubscriptionPlanGroups(duplicateUpgrade), "已自动可用")
}
