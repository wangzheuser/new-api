package service

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

//func GetPromptTokens(textRequest dto.GeneralOpenAIRequest, relayMode int) (int, error) {
//	switch relayMode {
//	case constant.RelayModeChatCompletions:
//		return CountTokenMessages(textRequest.Messages, textRequest.Model)
//	case constant.RelayModeCompletions:
//		return CountTokenInput(textRequest.Prompt, textRequest.Model), nil
//	case constant.RelayModeModerations:
//		return CountTokenInput(textRequest.Input, textRequest.Model), nil
//	}
//	return 0, errors.New("unknown relay mode")
//}

func ResponseText2Usage(c *gin.Context, responseText string, modeName string, promptTokens int) *dto.Usage {
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	usage := &dto.Usage{}
	usage.PromptTokens = promptTokens
	usage.CompletionTokens = EstimateTokenByModel(modeName, responseText)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

// EvaluateResponseOverrideBeforeSettlement records usage provenance and evaluates
// the complete candidate response immediately before billing settlement.
func EvaluateResponseOverrideBeforeSettlement(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, upstreamStatusCode int) *relaycommon.ResponseOverrideDecision {
	if c == nil || info == nil {
		return nil
	}
	if common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens) {
		info.SetResponseUsageState(relaycommon.ResponseUsageEstimated)
	} else if info.ResponseSemantics.Response.UsageState != relaycommon.ResponseUsageUpstream {
		if usage != nil && usage.TotalTokens > 0 {
			info.SetResponseUsageState(relaycommon.ResponseUsageEstimated)
		} else {
			info.SetResponseUsageState(relaycommon.ResponseUsageAbsent)
		}
	}
	if upstreamStatusCode == 0 {
		upstreamStatusCode = http.StatusOK
	}
	decision := relaycommon.EvaluateResponseOverride(c, upstreamStatusCode)
	if decision != nil && decision.ConfigError != "" {
		logger.LogWarn(c, fmt.Sprintf(
			"response override evaluation failed open: channel_id=%d channel_name=%q rule_id=%q error=%s",
			decision.ChannelID,
			decision.ChannelName,
			decision.RuleID,
			decision.ConfigError,
		))
	}
	return decision
}

func ValidUsage(usage *dto.Usage) bool {
	return usage != nil && (usage.PromptTokens != 0 || usage.CompletionTokens != 0)
}
