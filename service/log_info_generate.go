package service

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// attachQuotaSaturationToOther nests a quota saturation marker under
// other.admin_info.quota_saturation. Nesting under admin_info makes it
// admin-only for free, since model.formatUserLogs strips the whole admin_info
// object for non-admin viewers. Creates admin_info if absent. No-op when the
// clamp is nil (the common case: no saturation happened).
func attachQuotaSaturationToOther(other *model.LogOther, clamp *common.QuotaClamp) {
	if clamp == nil || other == nil {
		return
	}
	other.SetAdmin("quota_saturation", clamp.AuditMap())
}

// attachQuotaSaturation records the request's quota clamp (if any) onto the
// consume log's other.admin_info and emits a request-correlated backend audit
// line. Called right before RecordConsumeLog on the text/audio/wss paths.
func attachQuotaSaturation(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil {
		return
	}
	clamp := relayInfo.QuotaClamp
	if clamp == nil {
		return
	}
	attachQuotaSaturationToOther(other, clamp)
	logger.LogWarn(ctx, fmt.Sprintf("quota saturation on consume log: op=%s kind=%s original=%g clamped=%d user=%d model=%s",
		clamp.Op, clamp.Kind, clamp.Original, clamp.Clamped, relayInfo.UserId, relayInfo.GetBillingModelName()))
}

// appendBillingSettlementInfo records a failed settlement for admin reconciliation.
func appendBillingSettlementInfo(other *model.LogOther, relayInfo *relaycommon.RelayInfo) {
	if other == nil || relayInfo == nil || relayInfo.BillingSettlementState == "" {
		return
	}
	other.SetAdmin("billing_settlement", map[string]interface{}{
		"state":              relayInfo.BillingSettlementState,
		"error":              relayInfo.BillingSettlementError,
		"actual_quota":       relayInfo.BillingSettlementQuota,
		"pre_consumed_quota": relayInfo.FinalPreConsumedQuota,
	})
}

func appendRequestPath(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if other == nil {
		return
	}
	if ctx != nil && ctx.Request != nil && ctx.Request.URL != nil {
		if path := ctx.Request.URL.Path; path != "" {
			other.SetPublic("request_path", path)
			return
		}
	}
	if relayInfo != nil && relayInfo.RequestURLPath != "" {
		path := relayInfo.RequestURLPath
		if idx := strings.Index(path, "?"); idx != -1 {
			path = path[:idx]
		}
		other.SetPublic("request_path", path)
	}
}

// AppendRelayLogAdminInfo records relay routing and conversion diagnostics in
// the admin-only scope shared by successful and failed request logs.
func AppendRelayLogAdminInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if ctx == nil || other == nil {
		return
	}
	other.SetAdmin("use_channel", ctx.GetStringSlice("use_channel"))
	if relayInfo != nil {
		if billingModel := relayInfo.GetBillingModelName(); billingModel != "" && billingModel != relayInfo.OriginModelName {
			other.SetAdmin("billing_model", billingModel)
		}
		if diagnostics := relayInfo.ConversionDiagnostics(); len(diagnostics) > 0 {
			other.SetAdmin("conversion_diagnostics", diagnostics)
		}
		if relayInfo.ConversionDiagnosticsTruncated() {
			other.SetAdmin("conversion_diagnostics_truncated", true)
		}
	}
	if common.GetContextKeyBool(ctx, constant.ContextKeyChannelIsMultiKey) {
		other.SetAdmin("is_multi_key", true)
		other.SetAdmin("multi_key_index", common.GetContextKeyInt(ctx, constant.ContextKeyChannelMultiKeyIndex))
	}
	if common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens) {
		other.SetAdmin("local_count_tokens", true)
	}

	AppendChannelAffinityAdminInfo(ctx, other)
	if events := RequestPolicy(ctx).Events(); len(events) > 0 {
		other.SetAdmin("request_policy", events)
	}
}

// AppendResponseModelLogInfo records upstream model declarations for admin diagnostics.
func AppendResponseModelLogInfo(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil || relayInfo.ResponseModel == nil || relayInfo.ResponseModel.ReturnedModel == "" {
		return
	}
	if !relayInfo.ResponseModel.Mismatch() &&
		(relayInfo.ChannelMeta == nil || !relayInfo.ChannelMeta.IsModelMapped) &&
		relayInfo.ResponseModel.ReturnedModel == relayInfo.ResponseModel.RequestedModel &&
		(relayInfo.ResponseModel.UpstreamModel == "" || relayInfo.ResponseModel.ReturnedModel == relayInfo.ResponseModel.UpstreamModel) {
		return
	}
	other.SetAdmin("response_model", relayInfo.ResponseModel)
}

func GenerateTextOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelRatio, groupRatio, completionRatio float64,
	cacheTokens int, cacheRatio float64, modelPrice float64, userGroupRatio float64) *model.LogOther {
	MarkRequestPolicySuccess(ctx, relayInfo.StreamStatus)
	other := model.NewLogOther()
	other.SetPublic("model_ratio", modelRatio)
	other.SetPublic("group_ratio", groupRatio)
	other.SetPublic("completion_ratio", completionRatio)
	other.SetPublic("cache_tokens", cacheTokens)
	other.SetPublic("cache_ratio", cacheRatio)
	other.SetPublic("model_price", modelPrice)
	other.SetPublic("user_group_ratio", userGroupRatio)
	other.SetPublic("frt", float64(relayInfo.FirstResponseTime.UnixMilli()-relayInfo.StartTime.UnixMilli()))
	if relayInfo.ReasoningEffort != "" {
		other.SetPublic("reasoning_effort", relayInfo.ReasoningEffort)
	}
	if relayInfo.IsModelMapped && !relayInfo.IsContextFallbackActive() {
		other.SetPublic("is_model_mapped", true)
		other.SetPublic("upstream_model_name", relayInfo.UpstreamModelName)
	}

	isSystemPromptOverwritten := common.GetContextKeyBool(ctx, constant.ContextKeySystemPromptOverride)
	if isSystemPromptOverwritten {
		other.SetPublic("is_system_prompt_overwritten", true)
	}

	adminInfo := make(map[string]interface{})
	adminInfo["use_channel"] = ctx.GetStringSlice("use_channel")
	if common.GetContextKeyBool(ctx, constant.ContextKeySystemPromptApplied) {
		adminInfo["system_prompt"] = map[string]interface{}{
			"applied":         true,
			"source":          common.GetContextKeyString(ctx, constant.ContextKeySystemPromptSource),
			"matched_model":   common.GetContextKeyString(ctx, constant.ContextKeySystemPromptModel),
			"prepended":       isSystemPromptOverwritten,
			"injected_tokens": common.GetContextKeyInt(ctx, constant.ContextKeySystemPromptTokens),
		}
	}
	AppendContextFallbackAdminInfo(relayInfo, adminInfo)
	AppendResponseOverrideAdminInfo(relayInfo, adminInfo)
	if diagnostics := relayInfo.ConversionDiagnostics(); len(diagnostics) > 0 {
		adminInfo["conversion_diagnostics"] = diagnostics
		if relayInfo.ConversionDiagnosticsTruncated() {
			adminInfo["conversion_diagnostics_truncated"] = true
		}
	}
	isMultiKey := common.GetContextKeyBool(ctx, constant.ContextKeyChannelIsMultiKey)
	if isMultiKey {
		adminInfo["is_multi_key"] = true
		adminInfo["multi_key_index"] = common.GetContextKeyInt(ctx, constant.ContextKeyChannelMultiKeyIndex)
	}

	isLocalCountTokens := common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens)
	if isLocalCountTokens {
		adminInfo["local_count_tokens"] = isLocalCountTokens
	}

	AppendChannelAffinityAdminInfo(ctx, other)
	if events := RequestPolicy(ctx).Events(); len(events) > 0 {
		adminInfo["request_policy"] = events
	}
	AppendRelayProtocolInfo(relayInfo, other, adminInfo)
	AppendResponseModelLogInfo(relayInfo, other)

	other.MergeAdmin(adminInfo)
	appendRequestPath(ctx, relayInfo, other)
	appendBillingInfo(relayInfo, other)
	appendParamOverrideInfo(relayInfo, other)
	AppendStreamStatus(relayInfo, other)
	return other
}

// AppendRelayProtocolInfo adds route, normalization, conversion, and final format audit fields.
func AppendRelayProtocolInfo(relayInfo *relaycommon.RelayInfo, other *model.LogOther, adminInfo map[string]interface{}) {
	if relayInfo == nil {
		return
	}
	appendChannelRoutePlan(relayInfo, adminInfo)
	if adminInfo != nil && relayInfo.ProtocolNormalization != nil {
		adminInfo["protocol_normalization"] = relayInfo.ProtocolNormalization
	}
	appendRequestConversionChain(relayInfo, other)
	appendFinalRequestFormat(relayInfo, other)
}

// AppendResponseOverrideAdminInfo records the response-stage decision without exposing response bodies.
func AppendResponseOverrideAdminInfo(relayInfo *relaycommon.RelayInfo, adminInfo map[string]interface{}) {
	if relayInfo == nil || relayInfo.ResponseOverride == nil || adminInfo == nil {
		return
	}
	adminInfo["response_override"] = relayInfo.ResponseOverride
}

func appendChannelRoutePlan(relayInfo *relaycommon.RelayInfo, adminInfo map[string]interface{}) {
	if relayInfo == nil || adminInfo == nil {
		return
	}
	protocolRoute := map[string]interface{}{}
	if plan := relayInfo.ChannelRoutePlan; plan != nil {
		protocolRoute = map[string]interface{}{
			"mode":                      plan.RouteMode,
			"client_endpoint_type":      plan.ClientEndpointType,
			"upstream_endpoint_type":    plan.UpstreamEndpointType,
			"client_relay_format":       plan.ClientRelayFormat,
			"upstream_relay_format":     plan.UpstreamRelayFormat,
			"upstream_path":             plan.UpstreamPath,
			"quality":                   plan.Quality,
			"request_converter":         plan.RequestConverter,
			"request_normalizer":        plan.RequestNormalizer,
			"response_converter":        plan.ResponseConverter,
			"request_conversion_steps":  plan.RequestSteps,
			"response_conversion_steps": plan.ResponseSteps,
			"stream":                    plan.Stream,
			"capability_source":         plan.CapabilitySource,
		}
		if plan.RequestNormalizer != "" {
			protocolRoute["normalization_options"] = plan.NormalizationOptions
		}
	}
	if relayInfo.HasReasoningHistoryAudit() {
		protocolRoute["reasoning_history"] = relayInfo.ReasoningHistory
	}
	if len(protocolRoute) > 0 {
		adminInfo["protocol_route"] = protocolRoute
	}
}

// AppendContextFallbackAdminInfo 将上下文兜底路由细节放入管理员日志域。
func AppendContextFallbackAdminInfo(relayInfo *relaycommon.RelayInfo, adminInfo map[string]interface{}) {
	if relayInfo == nil || relayInfo.ContextFallback == nil || adminInfo == nil {
		return
	}
	decision := relayInfo.ContextFallback
	if !decision.Applied {
		if decision.BypassReason != "" {
			adminInfo["context_fallback"] = map[string]interface{}{
				"applied":       false,
				"bypass_reason": decision.BypassReason,
			}
		}
		return
	}
	adminInfo["context_fallback"] = map[string]interface{}{
		"applied":                        true,
		"reason":                         "context_threshold",
		"route_mode":                     decision.RouteMode,
		"requested_model":                relayInfo.GetRequestedModelName(),
		"billing_model":                  relayInfo.GetBillingModelName(),
		"source_model":                   decision.SourceModel,
		"attempt_model":                  decision.FallbackModel,
		"upstream_model":                 relayInfo.UpstreamModelName,
		"source_channel_id":              decision.SourceChannelID,
		"target_channel_id":              decision.TargetChannelID,
		"source_context_window_tokens":   decision.SourceContextWindowTokens,
		"fallback_context_window_tokens": decision.FallbackContextWindowTokens,
		"threshold_percent":              decision.ThresholdPercent,
		"threshold_tokens":               decision.ThresholdTokens,
		"source_base_input_tokens":       decision.SourceBaseInputTokens,
		"source_prompt_tokens":           decision.SourcePromptTokens,
		"source_output_reserve_tokens":   decision.SourceOutputReserveTokens,
		"source_demand_tokens":           decision.SourceDemandTokens,
		"target_base_input_tokens":       decision.TargetBaseInputTokens,
		"target_prompt_tokens":           decision.TargetPromptTokens,
		"target_output_reserve_tokens":   decision.TargetOutputReserveTokens,
		"target_demand_tokens":           decision.TargetDemandTokens,
	}
}

func appendParamOverrideInfo(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil || len(relayInfo.ParamOverrideAudit) == 0 {
		return
	}
	other.SetPublic("po", relayInfo.ParamOverrideAudit)
}

// AppendStreamStatus adds protocol terminal and transport end details to a log payload.
func AppendStreamStatus(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil || !relayInfo.IsStream {
		return
	}
	if relayInfo.StreamStatus == nil {
		other.SetPublic("stream_status", map[string]interface{}{
			"status":               "error",
			"phase":                "pre_stream",
			"end_reason":           "not_started",
			"received_event_count": relayInfo.ReceivedResponseCount,
			"terminal_seen":        false,
		})
		return
	}
	ss := relayInfo.StreamStatus
	reason, endErr := ss.End()
	terminalEvent, terminalStatus := ss.Terminal()
	errorCount, errorMessages := ss.ErrorMessages()
	status := "ok"
	if !ss.IsNormalEnd() || ss.HasErrors() || terminalStatus == "failed" {
		status = "error"
	}
	streamInfo := map[string]interface{}{
		"status":                     status,
		"phase":                      "streaming",
		"end_reason":                 string(reason),
		"received_event_count":       relayInfo.ReceivedResponseCount,
		"terminal_seen":              terminalEvent != "",
		"app_http_committed":         ss.AppHTTPIsCommitted(),
		"client_payload_committed":   ss.ClientPayloadIsCommitted(),
		"error_frame_written":        ss.ErrorFrameIsWritten(),
		"stream_policy_version":      ss.StreamPolicyVersion(),
		"retry_commit_policy":        ss.RetryCommitPolicy(),
		"billing_finalization":       ss.GetBillingFinalization(),
		"first_meaningful_byte_ms":   ss.FirstMeaningfulByteDuration(relayInfo.StartTime).Milliseconds(),
		"request_upload_duration_ms": relayInfo.RequestUploadDuration.Milliseconds(),
		"request_body_bytes":         relayInfo.IncomingRequestBodyBytes,
		"origin_handler_started":     !relayInfo.OriginHandlerStartedAt.IsZero(),
	}
	toolNameBytes, toolArgumentBytes := ss.ToolPayloadBytes()
	streamInfo["partial_tool_name_bytes"] = toolNameBytes
	streamInfo["partial_tool_argument_bytes"] = toolArgumentBytes
	if partialUsage, ok, estimated := ss.PartialUsageSnapshot(); ok {
		streamInfo["partial_usage_estimated"] = estimated
		streamInfo["partial_prompt_tokens"] = partialUsage.PromptTokens
		streamInfo["partial_completion_tokens"] = partialUsage.CompletionTokens
	}
	if endErr != nil {
		streamInfo["end_error"] = endErr.Error()
	}
	if terminalEvent != "" {
		streamInfo["terminal_event"] = terminalEvent
	}
	if terminalStatus != "" {
		streamInfo["terminal_status"] = terminalStatus
		streamInfo["response_status"] = terminalStatus
	}
	if errorCount > 0 {
		streamInfo["error_count"] = errorCount
		if relayInfo.RelayFormat == types.RelayFormatOpenAIResponses || relayInfo.RelayFormat == types.RelayFormatOpenAIResponsesCompaction {
			streamInfo["errors"] = []string{"upstream response stream failed"}
		} else {
			streamInfo["errors"] = errorMessages
		}
	}
	other.SetPublic("stream_status", streamInfo)
}

func appendBillingInfo(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil {
		return
	}
	// billing_source: "wallet" or "subscription"
	if relayInfo.BillingSource != "" {
		other.SetPublic("billing_source", relayInfo.BillingSource)
	}
	if relayInfo.UserSetting.BillingPreference != "" {
		other.SetPublic("billing_preference", relayInfo.UserSetting.BillingPreference)
	}
	if relayInfo.BillingSource == "subscription" {
		if relayInfo.SubscriptionId != 0 {
			other.SetPublic("subscription_id", relayInfo.SubscriptionId)
		}
		if relayInfo.SubscriptionPreConsumed > 0 {
			other.SetPublic("subscription_pre_consumed", relayInfo.SubscriptionPreConsumed)
		}
		// post_delta: settlement delta applied after actual usage is known (can be negative for refund)
		if relayInfo.SubscriptionPostDelta != 0 {
			other.SetPublic("subscription_post_delta", relayInfo.SubscriptionPostDelta)
		}
		if relayInfo.SubscriptionPlanId != 0 {
			other.SetPublic("subscription_plan_id", relayInfo.SubscriptionPlanId)
		}
		if relayInfo.SubscriptionPlanTitle != "" {
			other.SetPublic("subscription_plan_title", relayInfo.SubscriptionPlanTitle)
		}
		// Compute "this request" subscription consumed + remaining
		consumed := relayInfo.SubscriptionPreConsumed + relayInfo.SubscriptionPostDelta
		usedFinal := relayInfo.SubscriptionAmountUsedAfterPreConsume + relayInfo.SubscriptionPostDelta
		if consumed < 0 {
			consumed = 0
		}
		if usedFinal < 0 {
			usedFinal = 0
		}
		if relayInfo.SubscriptionAmountTotal > 0 {
			remain := max(relayInfo.SubscriptionAmountTotal-usedFinal, 0)
			other.SetPublic("subscription_total", relayInfo.SubscriptionAmountTotal)
			other.SetPublic("subscription_used", usedFinal)
			other.SetPublic("subscription_remain", remain)
		}
		if consumed > 0 {
			other.SetPublic("subscription_consumed", consumed)
		}
		// Wallet quota is not deducted when billed from subscription.
		other.SetPublic("wallet_quota_deducted", 0)
	}
}

func appendRequestConversionChain(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil {
		return
	}
	if len(relayInfo.RequestConversionChain) == 0 {
		return
	}
	chain := make([]string, 0, len(relayInfo.RequestConversionChain))
	for _, f := range relayInfo.RequestConversionChain {
		switch f {
		case types.RelayFormatOpenAI:
			chain = append(chain, "OpenAI Compatible")
		case types.RelayFormatClaude:
			chain = append(chain, "Claude Messages")
		case types.RelayFormatGemini:
			chain = append(chain, "Google Gemini")
		case types.RelayFormatOpenAIResponses:
			chain = append(chain, "OpenAI Responses")
		default:
			chain = append(chain, string(f))
		}
	}
	if len(chain) == 0 {
		return
	}
	other.SetPublic("request_conversion", chain)
}

func appendFinalRequestFormat(relayInfo *relaycommon.RelayInfo, other *model.LogOther) {
	if relayInfo == nil || other == nil {
		return
	}
	finalFormat := relayInfo.GetFinalRequestRelayFormat()
	if finalFormat == "" {
		return
	}
	other.SetPublic("final_request_relay_format", finalFormat)
	if finalFormat == types.RelayFormatClaude {
		// claude indicates the final upstream request format is Claude Messages.
		// Frontend log rendering uses this to keep the original Claude input display.
		other.SetPublic("claude", true)
	}
}

func GenerateWssOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage, modelRatio, groupRatio, completionRatio, audioRatio, audioCompletionRatio, modelPrice, userGroupRatio float64) *model.LogOther {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, 0, 0.0, modelPrice, userGroupRatio)
	info.SetPublic("ws", true)
	info.SetPublic("audio_input", usage.InputTokenDetails.AudioTokens)
	info.SetPublic("audio_output", usage.OutputTokenDetails.AudioTokens)
	info.SetPublic("text_input", usage.InputTokenDetails.TextTokens)
	info.SetPublic("text_output", usage.OutputTokenDetails.TextTokens)
	info.SetPublic("audio_ratio", audioRatio)
	info.SetPublic("audio_completion_ratio", audioCompletionRatio)
	return info
}

func GenerateAudioOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, modelRatio, groupRatio, completionRatio, audioRatio, audioCompletionRatio, modelPrice, userGroupRatio float64) *model.LogOther {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, 0, 0.0, modelPrice, userGroupRatio)
	info.SetPublic("audio", true)
	info.SetPublic("audio_input", usage.PromptTokensDetails.AudioTokens)
	info.SetPublic("audio_output", usage.CompletionTokenDetails.AudioTokens)
	info.SetPublic("text_input", usage.PromptTokensDetails.TextTokens)
	info.SetPublic("text_output", usage.CompletionTokenDetails.TextTokens)
	info.SetPublic("audio_ratio", audioRatio)
	info.SetPublic("audio_completion_ratio", audioCompletionRatio)
	return info
}

func GenerateClaudeOtherInfo(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelRatio, groupRatio, completionRatio float64,
	cacheTokens int, cacheRatio float64,
	cacheCreationTokens int, cacheCreationRatio float64,
	cacheCreationTokens5m int, cacheCreationRatio5m float64,
	cacheCreationTokens1h int, cacheCreationRatio1h float64,
	modelPrice float64, userGroupRatio float64) *model.LogOther {
	info := GenerateTextOtherInfo(ctx, relayInfo, modelRatio, groupRatio, completionRatio, cacheTokens, cacheRatio, modelPrice, userGroupRatio)
	info.SetPublic("claude", true)
	info.SetPublic("cache_creation_tokens", cacheCreationTokens)
	info.SetPublic("cache_creation_ratio", cacheCreationRatio)
	if cacheCreationTokens5m != 0 {
		info.SetPublic("cache_creation_tokens_5m", cacheCreationTokens5m)
		info.SetPublic("cache_creation_ratio_5m", cacheCreationRatio5m)
	}
	if cacheCreationTokens1h != 0 {
		info.SetPublic("cache_creation_tokens_1h", cacheCreationTokens1h)
		info.SetPublic("cache_creation_ratio_1h", cacheCreationRatio1h)
	}
	return info
}

func GenerateMjOtherInfo(relayInfo *relaycommon.RelayInfo, priceData hosttypes.PriceData) *model.LogOther {
	other := model.NewLogOther()
	other.SetPublic("model_price", priceData.ModelPrice)
	other.SetPublic("group_ratio", priceData.GroupRatioInfo.GroupRatio)
	if priceData.GroupRatioInfo.HasSpecialRatio {
		other.SetPublic("user_group_ratio", priceData.GroupRatioInfo.GroupSpecialRatio)
	}
	appendRequestPath(nil, relayInfo, other)
	return other
}

// InjectTieredBillingInfo overlays tiered billing fields onto an existing
// module-specific other map. Call this after GenerateTextOtherInfo /
// GenerateClaudeOtherInfo / etc. when the request used tiered_expr billing.
func InjectTieredBillingInfo(other *model.LogOther, relayInfo *relaycommon.RelayInfo, result *billingexpr.TieredResult) {
	if relayInfo == nil || other == nil {
		return
	}
	snap := relayInfo.TieredBillingSnapshot
	if snap == nil {
		return
	}
	other.SetPublic("billing_mode", "tiered_expr")
	other.SetPublic("expr_b64", base64.StdEncoding.EncodeToString([]byte(snap.ExprString)))
	if result != nil {
		if tokens := result.BillingTokens; tokens != nil && result.BillingUnit == billingexpr.BillingUnitToken {
			other.SetPublic("image_cache_tokens", tokens.ImgCR)
			other.SetPublic("billing_tokens", map[string]float64{
				"p": tokens.P, "c": tokens.C, "len": tokens.Len,
				"cr": tokens.CR, "cc": tokens.CC, "cc1h": tokens.CC1h,
				"img": tokens.Img, "img_cr": tokens.ImgCR, "img_o": tokens.ImgO,
				"ai": tokens.AI, "ao": tokens.AO,
			})
		}
		if result.ImageCount != nil {
			other.SetPublic("image_count", *result.ImageCount)
		}
		other.SetPublic("matched_tier", result.MatchedTier)
		if result.BillingUnit != "" {
			other.SetPublic("billing_unit", result.BillingUnit)
		}
		if result.FixedPrice != nil {
			other.SetPublic("fixed_price", *result.FixedPrice)
		}
		if len(result.RequestRules) > 0 {
			other.SetPublic("request_rules", result.RequestRules)
		}
	} else if snap.EstimatedBillingUnit != "" {
		if snap.EstimatedImageCount != nil {
			other.SetPublic("image_count", *snap.EstimatedImageCount)
		}
		other.SetPublic("matched_tier", snap.EstimatedTier)
		other.SetPublic("billing_unit", snap.EstimatedBillingUnit)
		if snap.EstimatedFixedPrice != nil {
			other.SetPublic("fixed_price", *snap.EstimatedFixedPrice)
		}
	}
}
