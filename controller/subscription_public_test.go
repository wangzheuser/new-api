package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPublicSubscriptionPlanDTOHidesProviderProductIDs verifies purchase
// capabilities replace the provider-specific identifiers in ordinary responses.
func TestPublicSubscriptionPlanDTOHidesProviderProductIDs(t *testing.T) {
	plan := model.SubscriptionPlan{
		Id:                    7,
		Title:                 "Public plan",
		AllowBalancePay:       common.GetPointer(true),
		AllowWalletOverflow:   common.GetPointer(true),
		StripePriceId:         "price_internal",
		CreemProductId:        "product_internal",
		WaffoPancakeProductId: "waffo_internal",
		Enabled:               true,
		SortOrder:             99,
		CreatedAt:             100,
		UpdatedAt:             200,
	}

	public := publicSubscriptionPlanDTO(plan)
	assert.ElementsMatch(t, []string{"balance", "stripe", "creem", "waffo_pancake"}, public.Plan.AvailablePaymentMethods)

	encoded, err := common.Marshal(public)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "price_internal")
	assert.NotContains(t, string(encoded), "product_internal")
	assert.NotContains(t, string(encoded), "waffo_internal")
	assert.NotContains(t, string(encoded), "sort_order")
	assert.NotContains(t, string(encoded), "created_at")
	assert.NotContains(t, string(encoded), "updated_at")
}
