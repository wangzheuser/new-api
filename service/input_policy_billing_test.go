package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inputPolicyBillingInfo provides explicit prices and deterministic independent cache draws.
func inputPolicyBillingInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI, OriginModelName: "m", StartTime: time.Now(),
		ChannelMeta:          &relaycommon.ChannelMeta{ChannelId: 1},
		PriceData:            types.PriceData{ModelRatio: 1, CompletionRatio: 2, CacheRatio: 0.1, CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
		ContextTruncation:    &relaycommon.ContextTruncationState{Applied: true, Before: 1000, After: 100},
		CacheUsageSimulation: &relaycommon.CacheUsageState{Policy: dto.CacheUsageSimulationPolicy{}, Presence: "absent", HasInput: true, Samples: [2]int{}},
	}
}

// TestInputPolicyBillingSemanticMatrix verifies normalized billing and unchanged client usage.
func TestInputPolicyBillingSemanticMatrix(t *testing.T) {
	ctx := newEntitlementBillingContext()
	for _, semantic := range []string{"openai", "anthropic", "gemini"} {
		t.Run(semantic, func(t *testing.T) {
			info := inputPolicyBillingInfo()
			original := &dto.Usage{PromptTokens: 100, CompletionTokens: 10, UsageSemantic: semantic}
			billing := applyContextTruncationBilling(ctx, info, original)
			billing = applyCacheUsageSimulation(ctx, info, billing)
			assert.Equal(t, 100, original.PromptTokens)
			assert.Zero(t, original.PromptTokensDetails.CachedTokens)
			assert.Equal(t, 300, billing.PromptTokensDetails.CachedCreationTokens)
			assert.Equal(t, 500, billing.PromptTokensDetails.CachedTokens)
			summary := calculateTextQuotaSummary(ctx, info, billing)
			// 200 ordinary + 300*1.25 creation + 500*0.1 reads + 10*2 output.
			assert.Equal(t, 645, summary.Quota)
			params := BuildTieredTokenParams(billing, semantic == "anthropic", billingexpr.UsedVars("p+c+cr+cc+len"))
			assert.EqualValues(t, 1000, params.Len)
		})
	}
}

// TestInputPolicyRealCacheAndPartialResponse ensures true cache and failed-stream semantics win.
func TestInputPolicyRealCacheAndPartialResponse(t *testing.T) {
	ctx := newEntitlementBillingContext()
	for _, state := range []string{"present", "invalid", "unknown"} {
		info := inputPolicyBillingInfo()
		info.CacheUsageSimulation.Presence = state
		usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 10, UsageSemantic: "openai"}
		got := applyCacheUsageSimulation(ctx, info, usage)
		assert.Same(t, usage, got)
		assert.False(t, info.CacheUsageSimulation.Applied)
	}
	info := inputPolicyBillingInfo()
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 10, UsageSemantic: "anthropic", PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 1100, CachedCreationTokens: 200}}
	got := applyContextTruncationBilling(ctx, info, usage)
	assert.Zero(t, got.PromptTokens)
	assert.Equal(t, 1300, info.ContextTruncation.Billed)
	assert.Equal(t, "estimate_conflict", info.ContextTruncation.Reason)
	got = applyCacheUsageSimulation(ctx, info, got)
	assert.Equal(t, 1100, got.PromptTokensDetails.CachedTokens)
	assert.False(t, info.CacheUsageSimulation.Applied)
	info = inputPolicyBillingInfo()
	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.SetBillingFinalization(relaycommon.BillingSettledPartial)
	got = applyCacheUsageSimulation(ctx, info, &dto.Usage{PromptTokens: 1000})
	assert.Zero(t, got.PromptTokensDetails.CachedTokens)
	assert.Equal(t, "partial_response", info.CacheUsageSimulation.Reason)
}

// TestInputPolicyWalletSettlement exercises actual SQLite wallet, token, consume log and duplicate finalization.
func TestInputPolicyWalletSettlement(t *testing.T) {
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "partial"}[partial], func(t *testing.T) {
			truncate(t)
			seedUser(t, 8101, 10000)
			seedToken(t, 8102, 8101, "input-policy-token", 10000)
			info := inputPolicyBillingInfo()
			info.UserId = 8101
			info.TokenId = 8102
			info.TokenKey = "input-policy-token"
			info.UsingGroup = "default"
			info.UserSetting = dto.UserSetting{BillingPreference: "wallet_only"}
			info.IsStream = true
			info.StreamStatus = relaycommon.NewStreamStatus()
			info.StreamStatus.MarkClientPayloadCommitted()
			ctx := newEntitlementBillingContext()
			ctx.Set("token_name", "input-policy-token")
			session, apiErr := NewBillingSession(ctx, info, 100)
			require.Nil(t, apiErr)
			info.Billing = session
			require.NoError(t, session.Reserve(1200))
			usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 10, UsageSemantic: "openai"}
			var relayErr *types.NewAPIError
			expected := 645
			if partial {
				relayErr = types.NewOpenAIError(errors.New("stream interrupted"), types.ErrorCodeBadResponse, 502)
				expected = 1020
			}
			_, err := FinalizeTextBilling(ctx, info, usage, relayErr)
			require.NoError(t, err)
			_, err = FinalizeTextBilling(ctx, info, usage, relayErr)
			require.NoError(t, err)
			quota, err := model.GetUserQuota(8101, true)
			require.NoError(t, err)
			assert.Equal(t, 10000-expected, quota)
			var token model.Token
			require.NoError(t, model.DB.First(&token, 8102).Error)
			assert.Equal(t, 10000-expected, token.RemainQuota)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("user_id = ?", 8101).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, expected, logs[0].Quota)
			var other map[string]interface{}
			require.NoError(t, common.UnmarshalJsonStr(logs[0].Other, &other))
			assert.Equal(t, true, other["context_truncated"])
			assert.Equal(t, !partial, info.CacheUsageSimulation.Applied)
			assert.Equal(t, 100, usage.PromptTokens)
		})
	}
}

// TestInputPolicyReserveCoversCreationSurcharge checks the highest possible cache classification.
func TestInputPolicyReserveCoversCreationSurcharge(t *testing.T) {
	info := inputPolicyBillingInfo()
	quota, err := EstimateCacheSimulationReserve(newEntitlementBillingContext(), info, 1000, 10)
	require.NoError(t, err)
	assert.Equal(t, 1095, quota)
}

// TestInputPolicyTieredBilling keeps length tiers and opt-in cache variables consistent across semantics.
func TestInputPolicyTieredBilling(t *testing.T) {
	ctx := newEntitlementBillingContext()
	for _, semantic := range []string{"openai", "anthropic"} {
		for _, tc := range []struct {
			expr  string
			quota int
		}{
			{`len >= 1000 ? tier("long", p * 2 + c * 4) : tier("short", p)`, 1020},
			{`len >= 1000 ? tier("long", p * 2 + c * 4 + cc * 2.5 + cr * 0.2) : tier("short", p)`, 645},
		} {
			info := inputPolicyBillingInfo()
			info.TieredBillingSnapshot = makeSnapshot(tc.expr, 1, 1000, 10)
			billing := applyContextTruncationBilling(ctx, info, &dto.Usage{PromptTokens: 100, CompletionTokens: 10, UsageSemantic: semantic})
			billing = applyCacheUsageSimulation(ctx, info, billing)
			params := BuildTieredTokenParams(billing, semantic == "anthropic", billingexpr.UsedVars(tc.expr))
			ok, quota, result := TryTieredSettle(info, params)
			require.True(t, ok)
			require.NotNil(t, result)
			expected := tc.quota
			// Existing Claude expression semantics keep P text-only even when cache variables are omitted.
			if semantic == "anthropic" && tc.quota == 1020 {
				expected = 220
			}
			assert.Equal(t, expected, quota)
			assert.Equal(t, "long", result.MatchedTier)
		}
	}
}

// TestInputPolicyNoTrimPreservesUpstreamBilling avoids replacing reported input when the rule did not run.
func TestInputPolicyNoTrimPreservesUpstreamBilling(t *testing.T) {
	info := inputPolicyBillingInfo()
	info.ContextTruncation.Applied = false
	info.CacheUsageSimulation = nil
	usage := &dto.Usage{PromptTokens: 5800}
	assert.Same(t, usage, applyContextTruncationBilling(newEntitlementBillingContext(), info, usage))
}

// TestInputPolicyRefundAndReserveFailure preserves wallet and token balances after a pre-payload failure.
func TestInputPolicyRefundAndReserveFailure(t *testing.T) {
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	truncate(t)
	seedUser(t, 8201, 1000)
	seedToken(t, 8202, 8201, "policy-refund-token", 500)
	info := inputPolicyBillingInfo()
	info.UserId = 8201
	info.TokenId = 8202
	info.TokenKey = "policy-refund-token"
	info.UserSetting = dto.UserSetting{BillingPreference: "wallet_only"}
	info.IsStream = true
	info.StreamStatus = relaycommon.NewStreamStatus()
	ctx := newEntitlementBillingContext()
	session, apiErr := NewBillingSession(ctx, info, 100)
	require.Nil(t, apiErr)
	info.Billing = session
	require.Error(t, session.Reserve(700)) // Wallet has enough; token does not. Funding must roll back.
	quota, err := model.GetUserQuota(8201, true)
	require.NoError(t, err)
	assert.Equal(t, 900, quota)
	failure := types.NewOpenAIError(errors.New("upstream failed before payload"), types.ErrorCodeBadResponse, 502)
	state, err := FinalizeTextBilling(ctx, info, nil, failure)
	require.NoError(t, err)
	assert.Equal(t, relaycommon.BillingRefunded, state)
	_, err = FinalizeTextBilling(ctx, info, nil, failure)
	require.NoError(t, err)
	require.Eventually(t, func() bool { quota, e := model.GetUserQuota(8201, true); return e == nil && quota == 1000 }, time.Second, 5*time.Millisecond)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 8202).Error)
	assert.Equal(t, 500, token.RemainQuota)
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ?", 8201).Count(&count).Error)
	assert.Zero(t, count)
}

// TestInputPolicyResponsesRetainsRealCache preserves native Responses input details through billing recovery.
func TestInputPolicyResponsesRetainsRealCache(t *testing.T) {
	raw := &dto.Usage{InputTokens: 100, OutputTokens: 10, InputTokensDetails: &dto.InputTokenDetails{CachedTokens: 40}}
	client := &dto.Usage{BillingUsage: dto.NewOpenAIResponsesBillingUsage(raw)}
	recovered := effectiveBillingUsage(client)
	require.NotNil(t, recovered)
	assert.Equal(t, 40, recovered.PromptTokensDetails.CachedTokens)
	info := inputPolicyBillingInfo()
	info.CacheUsageSimulation.Presence = "present"
	billed := applyContextTruncationBilling(newEntitlementBillingContext(), info, recovered)
	assert.Equal(t, 40, billed.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 100, raw.InputTokens)
	assert.Zero(t, raw.PromptTokensDetails.CachedTokens)
}
