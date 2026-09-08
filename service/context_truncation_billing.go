package service

import (
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// applyContextTruncationBilling copies effective usage and restores the untrimmed input budget.
func applyContextTruncationBilling(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) *dto.Usage {
	if info == nil || info.ContextTruncation == nil || !info.ContextTruncation.Applied {
		return usage
	}
	state := info.ContextTruncation
	summary := calculateTextQuotaSummary(c, info, usage)
	result := dto.Usage{}
	if usage != nil {
		result = *usage
	}
	creation := cacheWriteTokensTotal(summary)
	read := summary.CacheTokens
	total := summary.PromptTokens
	if summary.IsClaudeUsageSemantic {
		total = summary.InputTokens + creation + read
	}
	state.Upstream = total
	billed := max(state.Before, creation+read)
	state.Billed = billed
	if billed > state.Before {
		state.Reason = "estimate_conflict"
	}
	result.UsageSemantic = summary.UsageSemantic
	result.PromptTokensDetails.CachedTokens = read
	result.PromptTokensDetails.CachedCreationTokens = creation
	result.ClaudeCacheCreation5mTokens = max(summary.CacheCreationTokens5m, creation-summary.CacheCreationTokens1h)
	result.ClaudeCacheCreation1hTokens = summary.CacheCreationTokens1h
	result.InputPolicyAdjusted = true
	setInputPolicyTotal(&result, billed, summary.IsClaudeUsageSemantic)
	return &result
}

// setInputPolicyTotal preserves each protocol's cache-inclusive versus cache-exclusive input meaning.
func setInputPolicyTotal(usage *dto.Usage, total int, anthropic bool) {
	usage.PromptTokens = total
	if anthropic {
		usage.PromptTokens -= usage.PromptTokensDetails.CachedTokens + usage.PromptTokensDetails.CacheCreationTokensTotal()
	}
	usage.InputTokens = total
	usage.TotalTokens = total + usage.CompletionTokens
	if usage.InputTokensDetails != nil {
		details := usage.PromptTokensDetails
		usage.InputTokensDetails = &details
	}
}
