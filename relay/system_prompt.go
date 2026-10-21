package relay

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayhelper "github.com/QuantumNous/new-api/relay/helper"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// SystemPromptPreview 描述当前渠道尝试的提示词与上下文 Token 结果。
type SystemPromptPreview struct {
	BaseInputTokens     int
	PromptTokens        int
	FinalInputTokens    int
	OutputReserveTokens int
	Applied             bool
	Prepended           bool
	Source              string
	MatchedModel        string
}

// resolveSystemPrompt 解析当前渠道对原始请求模型生效的系统提示词。
func resolveSystemPrompt(info *relaycommon.RelayInfo) (string, bool, string, string) {
	if info == nil {
		return "", false, "", ""
	}
	settings := info.ChannelSetting
	return model_setting.ResolveSystemPrompt(dto.ChannelSettings{
		SystemPrompt:         settings.SystemPrompt,
		SystemPromptOverride: settings.SystemPromptOverride,
		ModelSystemPrompts:   settings.ModelSystemPrompts,
	},
		info.GetRequestedModelName(),
		info.GetAttemptModelName(),
		info.IsContextFallbackActive(),
	)
}

// PreviewSystemPromptTokens 在不修改原请求、计费会话和审计上下文的情况下预演当前渠道提示词。
func PreviewSystemPromptTokens(c *gin.Context, info *relaycommon.RelayInfo, request any, settings dto.ChannelSettings, attemptModel string, fallbackActive bool) (SystemPromptPreview, *types.NewAPIError) {
	if info == nil || request == nil {
		return SystemPromptPreview{}, nil
	}
	requestCopy, err := copySystemPromptRequest(request)
	if err != nil {
		return SystemPromptPreview{}, types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	previewInfo := *info
	previewInfo.SetAttemptModelName(attemptModel)
	previewInfo.OriginModelName = attemptModel
	previewInfo.ContextFallback = nil
	previewInfo.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelSetting:    relayChannelSettings(settings),
		UpstreamModelName: attemptModel,
	}
	requestCopy.SetModelName(attemptModel)

	prompt, prepend, source, matchedModel := model_setting.ResolveSystemPrompt(settings,
		info.GetRequestedModelName(),
		attemptModel,
		fallbackActive,
	)
	before := requestCopy.GetTokenCountMeta()
	applied, applyErr := applySystemPromptToRequest(nil, requestCopy, prompt, prepend)
	if applyErr != nil {
		return SystemPromptPreview{}, types.NewError(applyErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	after := requestCopy.GetTokenCountMeta()
	baseTokens, err := service.CountRequestToken(c, before, &previewInfo)
	if err != nil {
		return SystemPromptPreview{}, types.NewError(err, types.ErrorCodeCountTokenFailed, types.ErrOptionWithSkipRetry())
	}
	finalTokens, err := service.CountRequestToken(c, after, &previewInfo)
	if err != nil {
		return SystemPromptPreview{}, types.NewError(err, types.ErrorCodeCountTokenFailed, types.ErrOptionWithSkipRetry())
	}
	promptTokens := finalTokens - baseTokens
	if promptTokens < 0 {
		promptTokens = 0
	}
	return SystemPromptPreview{
		BaseInputTokens:     baseTokens,
		PromptTokens:        promptTokens,
		FinalInputTokens:    finalTokens,
		OutputReserveTokens: after.MaxTokens,
		Applied:             applied,
		Prepended:           applied && prepend,
		Source:              source,
		MatchedModel:        matchedModel,
	}, nil
}

// copySystemPromptRequest 仅复制已支持系统提示词和上下文兜底的文本请求。
func copySystemPromptRequest(request any) (dto.Request, error) {
	switch typedRequest := request.(type) {
	case *dto.GeneralOpenAIRequest:
		return common.DeepCopy(typedRequest)
	case *dto.OpenAIResponsesRequest:
		return common.DeepCopy(typedRequest)
	case *dto.OpenAIResponsesCompactionRequest:
		return common.DeepCopy(typedRequest)
	case *dto.ClaudeRequest:
		return common.DeepCopy(typedRequest)
	case *dto.GeminiChatRequest:
		return common.DeepCopy(typedRequest)
	case *kitdto.GeneralOpenAIRequest:
		return common.DeepCopy(typedRequest)
	case *kitdto.OpenAIResponsesRequest:
		return common.DeepCopy(typedRequest)
	case *kitdto.OpenAIResponsesCompactionRequest:
		return common.DeepCopy(typedRequest)
	case *kitdto.ClaudeRequest:
		return common.DeepCopy(typedRequest)
	case *kitdto.GeminiChatRequest:
		return common.DeepCopy(typedRequest)
	default:
		return nil, fmt.Errorf("context fallback does not handle request type %T", request)
	}
}

// applySystemPromptToRequest 使用同一份协议逻辑处理预演与实际请求。
func applySystemPromptToRequest(c *gin.Context, request dto.Request, systemPrompt string, prepend bool) (bool, error) {
	if systemPrompt == "" {
		return false, nil
	}
	// Root DTO callers are previews and legacy probes; production keeps its relaykit fields intact.
	var converted dto.Request
	switch typedRequest := request.(type) {
	case *kitdto.GeneralOpenAIRequest:
		return applyOpenAISystemPrompt(c, typedRequest, systemPrompt, prepend), nil
	case *kitdto.OpenAIResponsesRequest:
		return applyResponsesSystemPrompt(c, typedRequest, systemPrompt, prepend)
	case *kitdto.OpenAIResponsesCompactionRequest:
		responsesRequest := &kitdto.OpenAIResponsesRequest{Instructions: typedRequest.Instructions}
		applied, err := applyResponsesSystemPrompt(c, responsesRequest, systemPrompt, prepend)
		typedRequest.Instructions = responsesRequest.Instructions
		return applied, err
	case *kitdto.ClaudeRequest:
		return applyClaudeSystemPrompt(c, typedRequest, systemPrompt, prepend), nil
	case *kitdto.GeminiChatRequest:
		return applyGeminiSystemPrompt(c, typedRequest, systemPrompt, prepend), nil
	case *dto.GeneralOpenAIRequest:
		converted = &kitdto.GeneralOpenAIRequest{}
	case *dto.OpenAIResponsesRequest:
		converted = &kitdto.OpenAIResponsesRequest{}
	case *dto.OpenAIResponsesCompactionRequest:
		converted = &kitdto.OpenAIResponsesCompactionRequest{}
	case *dto.ClaudeRequest:
		converted = &kitdto.ClaudeRequest{}
	case *dto.GeminiChatRequest:
		converted = &kitdto.GeminiChatRequest{}
	default:
		return false, nil
	}
	data, err := common.Marshal(request)
	if err != nil {
		return false, err
	}
	if err := common.Unmarshal(data, converted); err != nil {
		return false, err
	}
	applied, err := applySystemPromptToRequest(c, converted, systemPrompt, prepend)
	if err != nil || !applied {
		return applied, err
	}
	data, err = common.Marshal(converted)
	if err != nil {
		return false, err
	}
	return true, common.Unmarshal(data, request)
}

// ApplySystemPromptForRequest 将渠道系统提示词应用到指定请求，并同步注入后的 Token 估算。
func ApplySystemPromptForRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) *types.NewAPIError {
	if info == nil || request == nil {
		return nil
	}

	systemPrompt, prepend, source, matchedModel := resolveSystemPrompt(info)
	if c != nil {
		common.SetContextKey(c, constant.ContextKeySystemPromptSource, source)
		common.SetContextKey(c, constant.ContextKeySystemPromptModel, matchedModel)
	}
	bypassReason := ""
	switch {
	case model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled:
		bypassReason = "pass_through"
	case strings.TrimSpace(systemPrompt) == "":
		bypassReason = "not_matched"
	}
	if bypassReason != "" {
		if c != nil {
			common.SetContextKey(c, constant.ContextKeySystemPromptBypassReason, bypassReason)
		}
		return nil
	}
	before := request.GetTokenCountMeta()
	applied, err := applySystemPromptToRequest(c, request, systemPrompt, prepend)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	if !applied {
		if c != nil {
			common.SetContextKey(c, constant.ContextKeySystemPromptBypassReason, "client_system_prompt")
		}
		return nil
	}
	if c != nil {
		common.SetContextKey(c, constant.ContextKeySystemPromptApplied, true)
		common.SetContextKey(c, constant.ContextKeySystemPromptBypassReason, "")
	}
	return accountInjectedSystemPrompt(c, info, before, request.GetTokenCountMeta())
}

// accountInjectedSystemPrompt 将实际注入的文本增量同步到本地估算与预扣费。
func accountInjectedSystemPrompt(c *gin.Context, info *relaycommon.RelayInfo, before, after *types.TokenCountMeta) *types.NewAPIError {
	if info == nil || before == nil || after == nil {
		return nil
	}
	beforeText := *before
	beforeText.Files = nil
	afterText := *after
	afterText.Files = nil
	beforeTokens, err := service.CountRequestToken(c, &beforeText, info)
	if err != nil {
		return types.NewError(err, types.ErrorCodeCountTokenFailed, types.ErrOptionWithSkipRetry())
	}
	afterTokens, err := service.CountRequestToken(c, &afterText, info)
	if err != nil {
		return types.NewError(err, types.ErrorCodeCountTokenFailed, types.ErrOptionWithSkipRetry())
	}
	delta := max(afterTokens-beforeTokens, 0)
	info.SetInjectedPromptTokenDelta(delta)
	if c != nil {
		common.SetContextKey(c, constant.ContextKeySystemPromptTokens, delta)
	}
	if delta <= 0 {
		return nil
	}
	if info.Billing == nil || info.IsChannelTest {
		return nil
	}

	quotaToReserve, err := relayhelper.EstimateQuotaWithFrozenPrice(info, info.GetEstimatePromptTokens(), after)
	if err != nil {
		return types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
	}
	if err := info.Billing.Reserve(quotaToReserve); err != nil {
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
	return nil
}

// markSystemPromptOverridden 记录渠道提示词已前置到客户端系统提示词。
func markSystemPromptOverridden(c *gin.Context) {
	if c != nil {
		common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
	}
}

// applyOpenAISystemPrompt 将渠道提示词应用到 OpenAI Chat Completions 请求。
func applyOpenAISystemPrompt(c *gin.Context, request *kitdto.GeneralOpenAIRequest, systemPrompt string, prepend bool) bool {
	if request == nil || systemPrompt == "" {
		return false
	}

	systemRole := request.GetSystemRoleName()
	for i, message := range request.Messages {
		// Dynamic tool-loading messages declare tools, not system instructions.
		if message.Role != systemRole || len(message.Tools) > 0 {
			continue
		}
		if !prepend {
			return false
		}

		markSystemPromptOverridden(c)
		if message.IsStringContent() {
			request.Messages[i].SetStringContent(systemPrompt + "\n" + message.StringContent())
			return true
		}
		contents := message.ParseContent()
		request.Messages[i].Content = append([]kitdto.MediaContent{{
			Type: dto.ContentTypeText,
			Text: systemPrompt,
		}}, contents...)
		return true
	}

	request.Messages = append([]kitdto.Message{{
		Role:    systemRole,
		Content: systemPrompt,
	}}, request.Messages...)
	return true
}

// applyResponsesSystemPrompt 将渠道提示词应用到 OpenAI Responses 请求。
func applyResponsesSystemPrompt(c *gin.Context, request *kitdto.OpenAIResponsesRequest, systemPrompt string, prepend bool) (bool, error) {
	if request == nil || systemPrompt == "" {
		return false, nil
	}

	if len(request.Instructions) == 0 {
		instructions, err := common.Marshal(systemPrompt)
		if err != nil {
			return false, err
		}
		request.Instructions = instructions
		return true, nil
	}
	if !prepend {
		return false, nil
	}

	markSystemPromptOverridden(c)
	var existing string
	if err := common.Unmarshal(request.Instructions, &existing); err != nil {
		return false, fmt.Errorf("responses instructions must be a string when system prompt prepend is enabled: %w", err)
	}
	existing = strings.TrimSpace(existing)
	if existing != "" {
		systemPrompt += "\n" + existing
	}
	instructions, err := common.Marshal(systemPrompt)
	if err != nil {
		return false, err
	}
	request.Instructions = instructions
	return true, nil
}

// applyClaudeSystemPrompt 将渠道提示词应用到 Claude Messages 请求。
func applyClaudeSystemPrompt(c *gin.Context, request *kitdto.ClaudeRequest, systemPrompt string, prepend bool) bool {
	if request == nil || systemPrompt == "" {
		return false
	}

	if request.System == nil {
		request.SetStringSystem(systemPrompt)
		return true
	}
	if !prepend {
		return false
	}

	markSystemPromptOverridden(c)
	if request.IsStringSystem() {
		existing := strings.TrimSpace(request.GetStringSystem())
		if existing == "" {
			request.SetStringSystem(systemPrompt)
		} else {
			request.SetStringSystem(systemPrompt + "\n" + existing)
		}
		return true
	}

	newSystem := kitdto.ClaudeMediaMessage{Type: dto.ContentTypeText}
	newSystem.SetText(systemPrompt)
	systemContents := request.ParseSystem()
	request.System = append([]kitdto.ClaudeMediaMessage{newSystem}, systemContents...)
	return true
}

// applyGeminiSystemPrompt 将渠道提示词应用到 Gemini GenerateContent 请求。
func applyGeminiSystemPrompt(c *gin.Context, request *kitdto.GeminiChatRequest, systemPrompt string, prepend bool) bool {
	if request == nil || systemPrompt == "" {
		return false
	}

	if request.SystemInstructions == nil {
		request.SystemInstructions = &kitdto.GeminiChatContent{
			Parts: []kitdto.GeminiPart{{Text: systemPrompt}},
		}
		return true
	}
	if len(request.SystemInstructions.Parts) == 0 {
		request.SystemInstructions.Parts = []kitdto.GeminiPart{{Text: systemPrompt}}
		return true
	}
	if !prepend {
		return false
	}

	markSystemPromptOverridden(c)
	for i := range request.SystemInstructions.Parts {
		if request.SystemInstructions.Parts[i].Text == "" {
			continue
		}
		request.SystemInstructions.Parts[i].Text = systemPrompt + "\n" + request.SystemInstructions.Parts[i].Text
		return true
	}
	request.SystemInstructions.Parts = append([]kitdto.GeminiPart{{Text: systemPrompt}}, request.SystemInstructions.Parts...)
	return true
}

func relayChannelSettings(settings dto.ChannelSettings) kitdto.ChannelSettings {
	var converted kitdto.ChannelSettings
	data, err := common.Marshal(settings)
	if err == nil {
		_ = common.Unmarshal(data, &converted)
	}
	return converted
}
