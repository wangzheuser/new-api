package controller

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// prepareContextFallback 预演源渠道和最终渠道的提示词，并在首次预扣前锁定当前尝试模型。
func prepareContextFallback(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request, initialTokens int) (*types.TokenCountMeta, *types.NewAPIError) {
	meta := request.GetTokenCountMeta()
	settings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	rule, hasRule := settings.ResolveContextFallback(info.GetRequestedModelName())
	hasPromptSettings := strings.TrimSpace(settings.SystemPrompt) != "" || len(settings.ModelSystemPrompts) > 0
	if !hasRule && !hasPromptSettings {
		info.SetEstimatePromptTokens(initialTokens)
		return meta, nil
	}

	if !supportsContextFallback(info, request) {
		info.SetEstimatePromptTokens(initialTokens)
		if hasRule {
			info.ContextFallback = &relaycommon.ContextFallbackDecision{BypassReason: "unsupported_relay_mode"}
		}
		return meta, nil
	}
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || settings.PassThroughBodyEnabled {
		info.SetEstimatePromptTokens(initialTokens)
		if hasRule {
			info.ContextFallback = &relaycommon.ContextFallbackDecision{BypassReason: "pass_through"}
		}
		return meta, nil
	}
	if hasProviderStateReference(request) {
		info.SetEstimatePromptTokens(initialTokens)
		if hasRule {
			info.ContextFallback = &relaycommon.ContextFallbackDecision{BypassReason: "provider_state_reference"}
		}
		return meta, nil
	}

	sourcePreview, previewErr := relay.PreviewSystemPromptTokens(
		c,
		info,
		request,
		settings,
		info.GetRoutingModelName(),
		false,
	)
	if previewErr != nil {
		return nil, previewErr
	}
	applyContextPreview(info, sourcePreview)
	meta.MaxTokens = sourcePreview.OutputReserveTokens
	if !hasRule {
		return meta, nil
	}

	sourceChannelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	sourceDemand := int64(sourcePreview.FinalInputTokens) + int64(sourcePreview.OutputReserveTokens)
	decision := &relaycommon.ContextFallbackDecision{
		RouteMode:                   rule.RouteMode,
		SourceModel:                 info.GetRoutingModelName(),
		FallbackModel:               rule.FallbackModel,
		SourceChannelID:             sourceChannelID,
		SourceContextWindowTokens:   rule.SourceContextWindowTokens,
		FallbackContextWindowTokens: rule.FallbackContextWindowTokens,
		ThresholdPercent:            rule.EffectiveThresholdPercent(),
		ThresholdTokens:             rule.ThresholdTokens(),
		SourceBaseInputTokens:       sourcePreview.BaseInputTokens,
		SourcePromptTokens:          sourcePreview.PromptTokens,
		SourceOutputReserveTokens:   sourcePreview.OutputReserveTokens,
		SourceDemandTokens:          sourceDemand,
		TargetChannelIDs:            append([]int(nil), rule.TargetChannelIDs...),
	}
	if sourceDemand <= decision.ThresholdTokens {
		return meta, nil
	}

	decision.Applied = true
	info.ContextFallback = decision
	info.SetAttemptModelName(rule.FallbackModel)
	common.SetContextKey(c, constant.ContextKeyAttemptModel, rule.FallbackModel)

	if rule.RouteMode == dto.ContextFallbackModeSame {
		sourceChannel, err := model.CacheGetChannel(sourceChannelID)
		if err != nil {
			return nil, contextFallbackChannelError(err)
		}
		if !common.StringsContains(sourceChannel.GetModels(), rule.FallbackModel) {
			return nil, contextFallbackChannelError(fmt.Errorf("no available channel for the requested model"))
		}
		decision.TargetChannelID = sourceChannelID
	} else {
		if _, pinned := c.Get("specific_channel_id"); pinned {
			return nil, contextFallbackRouteError("specific channel is unavailable for this request")
		}
		targetChannel, channelErr := selectContextFallbackTarget(c, info, &service.RetryParam{
			Ctx:         c,
			TokenGroup:  contextFallbackRoutingGroup(c, info),
			ModelName:   rule.FallbackModel,
			RequestPath: c.Request.URL.Path,
			IsStream:    info.IsStream,
			Retry:       common.GetPointer(0),
		})
		if channelErr != nil {
			return nil, channelErr
		}
		if setupErr := middleware.SetupContextForSelectedChannel(c, targetChannel, rule.FallbackModel); setupErr != nil {
			return nil, setupErr
		}
		decision.TargetChannelID = targetChannel.Id
	}

	targetPreview, previewErr := prepareContextFallbackTarget(c, info, request)
	if previewErr != nil {
		return nil, previewErr
	}
	meta.MaxTokens = targetPreview.OutputReserveTokens
	return meta, nil
}

// prepareContextFallbackTarget 复核当前目标渠道的映射、提示词和最终上下文占用。
func prepareContextFallbackTarget(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) (relay.SystemPromptPreview, *types.NewAPIError) {
	decision := info.ContextFallback
	if decision == nil || !decision.Applied {
		return relay.SystemPromptPreview{}, contextFallbackChannelError(fmt.Errorf("context fallback decision is missing"))
	}
	targetSettings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	targetPreview, previewErr := relay.PreviewSystemPromptTokens(
		c,
		info,
		request,
		targetSettings,
		decision.FallbackModel,
		true,
	)
	if previewErr != nil {
		return relay.SystemPromptPreview{}, previewErr
	}
	targetDemand := int64(targetPreview.FinalInputTokens) + int64(targetPreview.OutputReserveTokens)
	decision.TargetBaseInputTokens = targetPreview.BaseInputTokens
	decision.TargetPromptTokens = targetPreview.PromptTokens
	decision.TargetOutputReserveTokens = targetPreview.OutputReserveTokens
	decision.TargetDemandTokens = targetDemand
	decision.TargetChannelID = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	if targetDemand > decision.FallbackContextWindowTokens {
		return relay.SystemPromptPreview{}, contextFallbackRouteError("request context exceeds the supported context window")
	}
	applyContextPreview(info, targetPreview)
	return targetPreview, nil
}

// supportsContextFallback 将首期范围限定为可在发送前完整计数的文本协议。
func supportsContextFallback(info *relaycommon.RelayInfo, request dto.Request) bool {
	if info == nil || request == nil || info.IsChannelTest {
		return false
	}
	switch request.(type) {
	case *dto.GeneralOpenAIRequest:
		return info.RelayMode == relayconstant.RelayModeChatCompletions
	case *dto.OpenAIResponsesRequest, *dto.OpenAIResponsesCompactionRequest, *dto.ClaudeRequest, *dto.GeminiChatRequest:
		return true
	default:
		return false
	}
}

// hasProviderStateReference 识别依赖上游会话状态且本地缺少完整历史的 Responses 请求。
func hasProviderStateReference(request dto.Request) bool {
	switch typedRequest := request.(type) {
	case *dto.OpenAIResponsesRequest:
		return typedRequest.PreviousResponseID != ""
	case *dto.OpenAIResponsesCompactionRequest:
		return typedRequest.PreviousResponseID != ""
	default:
		return false
	}
}

// applyContextPreview 将当前候选的客户端基线与提示词增量写入 RelayInfo。
func applyContextPreview(info *relaycommon.RelayInfo, preview relay.SystemPromptPreview) {
	info.SetEstimatePromptTokens(preview.BaseInputTokens)
	info.SetInjectedPromptTokenDelta(preview.PromptTokens)
}

// selectContextFallbackTarget 从冻结分组中选择跨渠道兜底目标，源渠道始终排除。
func selectContextFallbackTarget(c *gin.Context, info *relaycommon.RelayInfo, retryParam *service.RetryParam) (*model.Channel, *types.NewAPIError) {
	decision := info.ContextFallback
	if decision == nil {
		return nil, contextFallbackChannelError(fmt.Errorf("context fallback decision is missing"))
	}
	if retryParam == nil {
		return nil, contextFallbackChannelError(fmt.Errorf("context fallback retry parameters are missing"))
	}
	group := contextFallbackRoutingGroup(c, info)
	if len(decision.TargetChannelIDs) > 0 {
		used := usedChannelIDSet(c)
		for _, channelID := range decision.TargetChannelIDs {
			if channelID == decision.SourceChannelID {
				continue
			}
			if _, exists := used[channelID]; exists {
				continue
			}
			channel, err := model.CacheGetChannel(channelID)
			if err != nil || !contextFallbackTargetEligible(channel, group, decision.FallbackModel, c.Request.URL.Path, info.IsStream) {
				continue
			}
			return channel, nil
		}
		return nil, contextFallbackChannelError(fmt.Errorf("no available channel for the requested model"))
	}

	if retryParam.ExcludedChannelIDs == nil {
		retryParam.ExcludedChannelIDs = make(map[int]struct{})
	}
	retryParam.ExcludedChannelIDs[decision.SourceChannelID] = struct{}{}
	for retryValue := retryParam.GetRetry(); retryValue <= common.RetryTimes; retryValue++ {
		candidateParam := *retryParam
		candidateParam.TokenGroup = group
		candidateParam.ModelName = decision.FallbackModel
		candidateParam.RequestPath = c.Request.URL.Path
		candidateParam.IsStream = info.IsStream
		candidateParam.Retry = &retryValue
		channel, _, _, err := service.CacheGetRandomSatisfiedChannelWithRoute(&candidateParam)
		if err != nil {
			return nil, contextFallbackChannelError(err)
		}
		if channel != nil && channel.Id != decision.SourceChannelID {
			return channel, nil
		}
	}
	return nil, contextFallbackChannelError(fmt.Errorf("no available channel for the requested model"))
}

// contextFallbackRoutingGroup 冻结订阅分组或 auto 首次解析到的具体分组。
func contextFallbackRoutingGroup(c *gin.Context, info *relaycommon.RelayInfo) string {
	if info.SubscriptionEntitlementGroup != "" {
		return info.SubscriptionEntitlementGroup
	}
	if autoGroup := common.GetContextKeyString(c, constant.ContextKeyAutoGroup); autoGroup != "" {
		return autoGroup
	}
	return info.TokenGroup
}

// contextFallbackTargetEligible 校验显式目标渠道的启用状态、分组、模型和路径。
func contextFallbackTargetEligible(channel *model.Channel, group, modelName, requestPath string, stream bool) bool {
	if channel == nil || channel.Status != common.ChannelStatusEnabled {
		return false
	}
	if !common.StringsContains(channel.GetGroups(), group) || !common.StringsContains(channel.GetModels(), modelName) {
		return false
	}
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		config := channel.GetOtherSettings().AdvancedCustom
		if config == nil || !config.SupportsPathForModel(requestPath, modelName) {
			return false
		}
	}
	plan, err := service.PlanChannelProtocolRoute(channel, modelName, requestPath, stream)
	if err != nil {
		return false
	}
	_, _, _, isTextProtocol := service.ResolveClientTextProtocol(requestPath)
	return !isTextProtocol || plan != nil
}

// usedChannelIDSet 返回已实际发送过上游请求的渠道集合。
func usedChannelIDSet(c *gin.Context) map[int]struct{} {
	used := make(map[int]struct{})
	for _, rawChannelID := range c.GetStringSlice("use_channel") {
		channelID := common.String2Int(rawChannelID)
		if channelID > 0 {
			used[channelID] = struct{}{}
		}
	}
	return used
}

// contextFallbackChannelError 将目标渠道配置或选择失败转为统一路由错误。
func contextFallbackChannelError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeGetChannelFailed, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
}

// contextFallbackRouteError 返回发送前可确定的上下文路由约束错误。
func contextFallbackRouteError(message string) *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf("%s", message), types.ErrorCode("context_length_exceeded"), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}
