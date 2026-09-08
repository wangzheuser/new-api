package service

import (
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// applyCacheUsageSimulation classifies a billing-only copy after all raw usage observations finish.
func applyCacheUsageSimulation(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) *dto.Usage {
	if info == nil || info.CacheUsageSimulation == nil {
		return usage
	}
	state := info.CacheUsageSimulation
	if state.Reason != "" {
		return usage
	}
	if info.StreamStatus != nil && info.StreamStatus.GetBillingFinalization() == relaycommon.BillingSettledPartial {
		state.Reason = "partial_response"
		return usage
	}
	if state.Presence != "absent" {
		state.Reason = "upstream_cache_" + state.Presence
		return usage
	}
	if !state.HasInput || usage == nil {
		state.Reason = "missing_upstream_input"
		return usage
	}
	summary := calculateTextQuotaSummary(c, info, usage)
	if summary.CacheTokens > 0 || cacheWriteTokensTotal(summary) > 0 {
		state.Reason = "existing_cache_usage"
		return usage
	}
	if summary.ImageTokens > 0 || summary.AudioTokens > 0 {
		state.Reason = "multimodal"
		return usage
	}
	total := summary.PromptTokens
	if total <= 0 {
		state.Reason = "missing_upstream_input"
		return usage
	}
	creation, read := dto.SplitCacheUsage(total, state.Policy, state.Samples)
	state.Creation = creation
	state.Read = read
	state.Billed = total
	state.Applied = creation > 0 || read > 0
	if !state.Applied {
		state.Reason = "not_triggered"
		return usage
	}
	result := *usage
	result.UsageSemantic = summary.UsageSemantic
	result.InputPolicyAdjusted = true
	result.PromptTokensDetails.CachedCreationTokens = creation
	result.PromptTokensDetails.CachedTokens = read
	if summary.IsClaudeUsageSemantic {
		result.ClaudeCacheCreation5mTokens = creation
		result.ClaudeCacheCreation1hTokens = 0
	}
	setInputPolicyTotal(&result, total, summary.IsClaudeUsageSemantic)
	return &result
}

// EstimateCacheSimulationReserve compares possible classifications without sampling or settling funds.
func EstimateCacheSimulationReserve(c *gin.Context, info *relaycommon.RelayInfo, total, output int) (int, error) {
	state := info.CacheUsageSimulation
	if state == nil || state.Reason != "" || info.PriceData.UsePrice || info.PriceData.FreeModel {
		return 0, nil
	}
	if output <= 0 && info.TieredBillingSnapshot != nil {
		output = info.TieredBillingSnapshot.EstimatedCompletionTokens
	}
	highest := 0
	for _, sample := range [][2]int{{99, 99}, {0, 99}, {99, 0}, {0, 0}} {
		creation, read := dto.SplitCacheUsage(total, state.Policy, sample)
		usage := &dto.Usage{PromptTokens: total, CompletionTokens: output, UsageSemantic: "openai", InputPolicyAdjusted: true}
		usage.PromptTokensDetails.CachedTokens = read
		usage.PromptTokensDetails.CachedCreationTokens = creation
		quota := 0
		if snap := info.TieredBillingSnapshot; snap != nil {
			request := billingexpr.RequestInput{}
			if info.BillingRequestInput != nil {
				request = *info.BillingRequestInput
			}
			result, err := billingexpr.ComputeTieredQuotaWithRequest(snap, BuildTieredTokenParams(usage, false, billingexpr.UsedVars(snap.ExprString)), request)
			if err != nil {
				return 0, err
			}
			quota = result.ActualQuotaAfterGroup
		} else {
			quota = calculateTextQuotaSummary(c, info, usage).Quota
		}
		highest = max(highest, quota)
	}
	return highest, nil
}
