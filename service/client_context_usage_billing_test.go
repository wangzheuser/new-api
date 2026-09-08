package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClientContextUsageBillingIsolation reproduces the production counts without feeding client estimates back into billing.
func TestClientContextUsageBillingIsolation(t *testing.T) {
	ctx := newEntitlementBillingContext()
	for _, before := range []int{275003, 100000} {
		info := inputPolicyBillingInfo()
		info.ContextTruncation.Before = before
		info.CacheUsageSimulation.Presence = "present"
		original := &dto.Usage{PromptTokens: 114482, CompletionTokens: 40, TotalTokens: 114522, UsageSemantic: "openai"}
		baseline := applyContextTruncationBilling(ctx, info, original)
		baselineQuota := calculateTextQuotaSummary(ctx, info, baseline).Quota
		body := []byte(`{"usage":{"prompt_tokens":114482,"completion_tokens":40,"total_tokens":114522}}`)
		_, _ = relaycommon.RewriteClientContextUsageJSON(body, types.RelayFormatOpenAI, info.ContextTruncation, 200)
		billing := applyContextTruncationBilling(ctx, info, original)
		billing = applyCacheUsageSimulation(ctx, info, billing)
		assert.Equal(t, baselineQuota, calculateTextQuotaSummary(ctx, info, billing).Quota)
		assert.Equal(t, before, info.ContextTruncation.Billed)
		assert.Equal(t, 114482, info.ContextTruncation.Upstream)
		assert.EqualValues(t, max(before, 114482), info.ContextTruncation.Reported)
		assert.Equal(t, 114482, original.PromptTokens)
		assert.EqualValues(t, before, BuildTieredTokenParams(billing, false, billingexpr.UsedVars("p+c+len")).Len)
		assert.False(t, info.CacheUsageSimulation.Applied)
		other := map[string]any{}
		AppendInputPolicyLog(other, info)
		admin, ok := other["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Same(t, info.ContextTruncation, admin["context_truncation"])
	}
}
