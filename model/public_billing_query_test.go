package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetAllPublicUserSubscriptionsSelectsOwnerFields verifies internal source
// and group rollback snapshots are absent from the ordinary subscription type.
func TestGetAllPublicUserSubscriptionsSelectsOwnerFields(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:              23,
		PlanId:              7,
		AmountTotal:         1000,
		AmountUsed:          250,
		AllocationCount:     2,
		StartTime:           now - 10,
		EndTime:             now + 3600,
		Status:              "active",
		Source:              "admin",
		NextResetTime:       now + 1800,
		EntitlementGroup:    "premium",
		GrantGroups:         GroupNames{"vip"},
		UpgradeGroup:        "operator-upgrade",
		PrevUserGroup:       "operator-previous",
		DowngradeGroup:      "operator-downgrade",
		AllowWalletOverflow: true,
	}).Error)

	all, err := GetAllPublicUserSubscriptions(23)
	require.NoError(t, err)
	require.Len(t, all, 1)
	public := all[0].Subscription
	require.NotNil(t, public)
	assert.Equal(t, 7, public.PlanId)
	assert.EqualValues(t, 250, public.AmountUsed)
	assert.Equal(t, GroupNames{"vip"}, public.GrantGroups)

	encoded, err := common.Marshal(all)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "user_id")
	assert.NotContains(t, string(encoded), "source")
	assert.NotContains(t, string(encoded), "prev_user_group")
	assert.NotContains(t, string(encoded), "upgrade_group")
	assert.NotContains(t, string(encoded), "downgrade_group")
}

// TestGetUserTopUpsSelectsOwnerFields verifies ordinary billing history omits
// the redundant user and internal gateway identifiers.
func TestGetUserTopUpsSelectsOwnerFields(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&TopUp{
		UserId:          23,
		Amount:          500,
		Money:           5,
		TradeNo:         "trade-public",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: "internal-provider",
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusSuccess,
	}).Error)

	page := &common.PageInfo{Page: 1, PageSize: 20}
	items, total, err := GetUserTopUps(23, page)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, "trade-public", items[0].TradeNo)

	encoded, err := common.Marshal(items)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "user_id")
	assert.NotContains(t, string(encoded), "payment_provider")
	assert.NotContains(t, string(encoded), "internal-provider")
}
