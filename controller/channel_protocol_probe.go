package controller

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/relaynormalize"
	"github.com/QuantumNous/new-api/types"

	"github.com/samber/lo"
)

type protocolProbeCase string

const (
	protocolProbeCaseBasic              protocolProbeCase = "basic"
	protocolProbeCaseAssistantHistory   protocolProbeCase = "assistant_history"
	protocolProbeCaseToolCycle          protocolProbeCase = "tool_cycle"
	protocolProbeCaseReasoningHistory   protocolProbeCase = "reasoning_history"
	protocolProbeCaseInvalidToolID      protocolProbeCase = "invalid_tool_id"
	protocolProbeCaseToolIDCollision    protocolProbeCase = "tool_id_collision"
	protocolProbeRecommendedNative                        = "native"
	protocolProbeRecommendedNormalized                    = "normalized"
	protocolProbeRecommendedUnsupported                   = "unsupported"
)

type protocolProbeExecution struct {
	RouteMode            types.ChannelRouteMode
	RequestNormalizer    string
	NormalizationOptions types.RequestNormalizationOptions
}

// parseProtocolProbeCase validates a requested probe scenario while preserving the legacy native probe as basic.
func parseProtocolProbeCase(raw string, enabled bool) (protocolProbeCase, error) {
	if !enabled {
		return "", nil
	}
	probeCase := protocolProbeCase(strings.TrimSpace(raw))
	if probeCase == "" {
		return protocolProbeCaseBasic, nil
	}
	switch probeCase {
	case protocolProbeCaseBasic,
		protocolProbeCaseAssistantHistory,
		protocolProbeCaseToolCycle,
		protocolProbeCaseReasoningHistory,
		protocolProbeCaseInvalidToolID,
		protocolProbeCaseToolIDCollision:
		return probeCase, nil
	default:
		return "", fmt.Errorf("unsupported protocol probe case: %s", raw)
	}
}

// IsSemantic reports whether the scenario verifies history semantics instead of endpoint reachability alone.
func (p protocolProbeCase) IsSemantic() bool {
	return p != "" && p != protocolProbeCaseBasic
}

// CapabilityLevel returns the product-level meaning of this probe result.
func (p protocolProbeCase) CapabilityLevel() string {
	if p.IsSemantic() {
		return "semantic"
	}
	return "endpoint"
}

// resolveProtocolProbeExecution chooses the exact direct route exercised by one non-persistent probe.
func resolveProtocolProbeExecution(
	channel *model.Channel,
	modelName string,
	endpointType constant.EndpointType,
	stream bool,
	probeCase protocolProbeCase,
) protocolProbeExecution {
	if !probeCase.IsSemantic() {
		return protocolProbeExecution{RouteMode: types.ChannelRouteModeNative}
	}

	if capability, ok := configuredProtocolProbeCapability(channel, modelName, endpointType, stream); ok {
		if capability.EffectiveMode() == dto.ProtocolHandlingModeNormalized {
			normalizer, supported := protocolProbeNormalizer(endpointType)
			if supported {
				options := types.RequestNormalizationOptions{}
				if endpointType == constant.EndpointTypeAnthropic {
					options.ReasoningHistoryPolicy = capability.EffectiveReasoningHistoryPolicy()
				}
				return protocolProbeExecution{
					RouteMode:            types.ChannelRouteModeNormalized,
					RequestNormalizer:    normalizer,
					NormalizationOptions: options,
				}
			}
		}
		return protocolProbeExecution{RouteMode: types.ChannelRouteModeNative}
	}

	normalizer, supported := protocolProbeNormalizer(endpointType)
	if !supported {
		return protocolProbeExecution{RouteMode: types.ChannelRouteModeNative}
	}
	options := types.RequestNormalizationOptions{}
	if endpointType == constant.EndpointTypeAnthropic {
		// Unconfigured standard-compatible Claude endpoints are probed with the compatibility-safe history policy.
		options.ReasoningHistoryPolicy = types.ReasoningHistoryPolicyStrip
	}
	return protocolProbeExecution{
		RouteMode:            types.ChannelRouteModeNormalized,
		RequestNormalizer:    normalizer,
		NormalizationOptions: options,
	}
}

// configuredProtocolProbeCapability resolves a saved model override before channel defaults.
func configuredProtocolProbeCapability(
	channel *model.Channel,
	modelName string,
	endpointType constant.EndpointType,
	stream bool,
) (dto.ProtocolCapability, bool) {
	if channel == nil {
		return dto.ProtocolCapability{}, false
	}
	policy := channel.GetSetting().ProtocolPolicy
	if policy == nil {
		return dto.ProtocolCapability{}, false
	}
	native, _ := policy.NativeForModel(modelName)
	capability, ok := native[endpointType]
	if !ok || stream && !capability.Stream || !stream && !capability.NonStream {
		return dto.ProtocolCapability{}, false
	}
	return capability, true
}

// protocolProbeNormalizer resolves the compatibility normalizers supported by persisted protocol capabilities.
func protocolProbeNormalizer(endpointType constant.EndpointType) (string, bool) {
	switch endpointType {
	case constant.EndpointTypeAnthropic:
		return relaynormalize.RequestNormalizerAnthropicMessagesCompatible, true
	case constant.EndpointTypeOpenAIResponse:
		return relaynormalize.RequestNormalizerOpenAIResponsesCompatible, true
	default:
		return "", false
	}
}

// recommendedProtocolProbeMode classifies a probe without changing persisted channel settings.
func recommendedProtocolProbeMode(
	channel *model.Channel,
	modelName string,
	endpointType constant.EndpointType,
	stream bool,
	probeCase protocolProbeCase,
	execution protocolProbeExecution,
	classification string,
) string {
	if classification != "confirmed" {
		return protocolProbeRecommendedUnsupported
	}
	if probeCase == protocolProbeCaseBasic {
		if _, supported := protocolProbeNormalizer(endpointType); supported {
			return protocolProbeRecommendedNormalized
		}
		return protocolProbeRecommendedUnsupported
	}
	if execution.RouteMode == types.ChannelRouteModeNormalized {
		return protocolProbeRecommendedNormalized
	}
	if capability, ok := configuredProtocolProbeCapability(channel, modelName, endpointType, stream); ok &&
		capability.EffectiveMode() == dto.ProtocolHandlingModeNative {
		return protocolProbeRecommendedNative
	}
	return protocolProbeRecommendedUnsupported
}

// protocolProbeRoutePlan records the actual direct probe route for audit and final-wire normalization.
func protocolProbeRoutePlan(
	endpointType constant.EndpointType,
	relayFormat types.RelayFormat,
	requestPath string,
	relayMode int,
	options channelTestOptions,
) *types.ChannelRoutePlan {
	return &types.ChannelRoutePlan{
		ClientEndpointType:   endpointType,
		UpstreamEndpointType: endpointType,
		ClientRelayFormat:    relayFormat,
		UpstreamRelayFormat:  relayFormat,
		ClientRelayMode:      relayMode,
		UpstreamRelayMode:    relayMode,
		ClientPath:           requestPath,
		UpstreamPath:         requestPath,
		RouteMode:            options.protocolProbeExecution.RouteMode,
		RequestNormalizer:    options.protocolProbeExecution.RequestNormalizer,
		NormalizationOptions: options.protocolProbeExecution.NormalizationOptions,
		Stream:               options.isStream,
		CapabilitySource:     "manual_probe",
	}
}

// buildProtocolProbeRequest creates the deterministic request body for one endpoint and compatibility scenario.
func buildProtocolProbeRequest(
	modelName string,
	endpointType constant.EndpointType,
	options channelTestOptions,
) (dto.Request, error) {
	switch endpointType {
	case constant.EndpointTypeAnthropic:
		return buildAnthropicProtocolProbeRequest(modelName, options), nil
	case constant.EndpointTypeOpenAIResponse:
		return buildResponsesProtocolProbeRequest(modelName, options)
	case constant.EndpointTypeGemini:
		return buildGeminiProtocolProbeRequest(options)
	case constant.EndpointTypeOpenAI:
		return buildOpenAIProtocolProbeRequest(modelName, options)
	default:
		return nil, errors.New("protocol probe requires a supported text endpoint")
	}
}

// buildAnthropicProtocolProbeRequest creates Claude Messages histories that exercise the registered wire normalizer.
func buildAnthropicProtocolProbeRequest(modelName string, options channelTestOptions) *dto.ClaudeRequest {
	request := &dto.ClaudeRequest{
		Model:     modelName,
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "Reply briefly."}},
		MaxTokens: lo.ToPtr(protocolProbeMaxTokens(options)),
		Stream:    lo.ToPtr(options.isStream),
	}
	switch options.protocolProbeCase {
	case protocolProbeCaseAssistantHistory:
		request.Messages = []dto.ClaudeMessage{
			{Role: "user", Content: "Continue the history."},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: lo.ToPtr("   ")}}},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: lo.ToPtr("Ready.")}}},
			{Role: "user", Content: "Reply briefly."},
		}
	case protocolProbeCaseReasoningHistory:
		request.Messages = []dto.ClaudeMessage{
			{Role: "user", Content: "Continue the history."},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "thinking", Thinking: lo.ToPtr("internal")}}},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{
				{Type: "thinking", Thinking: lo.ToPtr("internal")},
				{Type: "text", Text: lo.ToPtr("Ready.")},
			}},
			{Role: "user", Content: "Reply briefly."},
		}
	case protocolProbeCaseToolCycle:
		request.Messages = anthropicToolProbeMessages([]string{"toolu_probe"})
		request.Tools = []any{protocolProbeClaudeTool()}
	case protocolProbeCaseInvalidToolID:
		request.Messages = anthropicToolProbeMessages([]string{"call.id:invalid"})
		request.Tools = []any{protocolProbeClaudeTool()}
	case protocolProbeCaseToolIDCollision:
		request.Messages = anthropicToolProbeMessages([]string{"call.a", "call:a"})
		request.Tools = []any{protocolProbeClaudeTool()}
	}
	return request
}

// anthropicToolProbeMessages keeps tool calls and results in one deterministic, associated history cycle.
func anthropicToolProbeMessages(toolIDs []string) []dto.ClaudeMessage {
	toolUses := make([]dto.ClaudeMediaMessage, 0, len(toolIDs))
	toolResults := make([]dto.ClaudeMediaMessage, 0, len(toolIDs)+1)
	for _, toolID := range toolIDs {
		toolUses = append(toolUses, dto.ClaudeMediaMessage{
			Type: "tool_use", Id: toolID, Name: "probe_tool", Input: map[string]any{"value": "ok"},
		})
		toolResults = append(toolResults, dto.ClaudeMediaMessage{
			Type: "tool_result", ToolUseId: toolID, Content: "ok",
		})
	}
	toolResults = append(toolResults, dto.ClaudeMediaMessage{Type: "text", Text: lo.ToPtr("Reply briefly.")})
	return []dto.ClaudeMessage{
		{Role: "user", Content: "Use the supplied result."},
		{Role: "assistant", Content: toolUses},
		{Role: "user", Content: toolResults},
	}
}

// protocolProbeClaudeTool returns the shared function declaration used by history probes.
func protocolProbeClaudeTool() *dto.Tool {
	return &dto.Tool{
		Name: "probe_tool",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"value": map[string]interface{}{"type": "string"}},
		},
	}
}

// buildResponsesProtocolProbeRequest creates Responses input items for endpoint, history, and call-id validation.
func buildResponsesProtocolProbeRequest(modelName string, options channelTestOptions) (*dto.OpenAIResponsesRequest, error) {
	input := []map[string]any{{"role": "user", "content": "Reply briefly."}}
	request := &dto.OpenAIResponsesRequest{
		Model:           modelName,
		Stream:          lo.ToPtr(options.isStream),
		MaxOutputTokens: lo.ToPtr(protocolProbeMaxTokens(options)),
	}
	switch options.protocolProbeCase {
	case protocolProbeCaseAssistantHistory:
		input = []map[string]any{
			{"role": "user", "content": "Continue the history."},
			{"role": "assistant", "content": "   "},
			{"role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "Ready."}}},
			{"role": "user", "content": "Reply briefly."},
		}
	case protocolProbeCaseReasoningHistory:
		input = []map[string]any{
			{"role": "user", "content": "Continue the history."},
			{"role": "assistant", "content": []map[string]any{{"type": "output_text", "text": "Ready."}}},
			{"role": "user", "content": "Reply briefly."},
		}
	case protocolProbeCaseToolCycle:
		input = responsesToolProbeItems([]string{"call_probe"})
	case protocolProbeCaseInvalidToolID:
		input = responsesToolProbeItems([]string{"call.id:invalid"})
	case protocolProbeCaseToolIDCollision:
		input = responsesToolProbeItems([]string{"call.a", "call:a"})
	}
	var err error
	request.Input, err = common.Marshal(input)
	if err != nil {
		return nil, err
	}
	if options.protocolProbeCase == protocolProbeCaseToolCycle ||
		options.protocolProbeCase == protocolProbeCaseInvalidToolID ||
		options.protocolProbeCase == protocolProbeCaseToolIDCollision {
		request.Tools, err = protocolProbeResponsesTools()
		if err != nil {
			return nil, err
		}
	}
	return request, nil
}

// responsesToolProbeItems keeps function calls and outputs paired by their call_id.
func responsesToolProbeItems(toolIDs []string) []map[string]any {
	items := []map[string]any{{"role": "user", "content": "Use the supplied result."}}
	for index, toolID := range toolIDs {
		items = append(items, map[string]any{
			"type": "function_call", "id": fmt.Sprintf("fc_probe_%d", index), "call_id": toolID,
			"name": "probe_tool", "arguments": `{"value":"ok"}`,
		})
	}
	for _, toolID := range toolIDs {
		items = append(items, map[string]any{
			"type": "function_call_output", "call_id": toolID, "output": "ok",
		})
	}
	return append(items, map[string]any{"role": "user", "content": "Reply briefly."})
}

// protocolProbeResponsesTools marshals the shared Responses function declaration.
func protocolProbeResponsesTools() ([]byte, error) {
	return common.Marshal([]map[string]any{{
		"type": "function", "name": "probe_tool",
		"parameters": map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
		},
	}})
}

// buildOpenAIProtocolProbeRequest creates valid chat histories while leaving native capability selection explicit.
func buildOpenAIProtocolProbeRequest(modelName string, options channelTestOptions) (*dto.GeneralOpenAIRequest, error) {
	request := &dto.GeneralOpenAIRequest{
		Model:     modelName,
		Messages:  []dto.Message{{Role: "user", Content: "Reply briefly."}},
		MaxTokens: lo.ToPtr(protocolProbeMaxTokens(options)),
		Stream:    lo.ToPtr(options.isStream),
	}
	if options.isStream {
		request.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}
	switch options.protocolProbeCase {
	case protocolProbeCaseAssistantHistory, protocolProbeCaseReasoningHistory:
		request.Messages = []dto.Message{
			{Role: "user", Content: "Continue the history."},
			{Role: "assistant", Content: "Ready."},
			{Role: "user", Content: "Reply briefly."},
		}
	case protocolProbeCaseToolCycle, protocolProbeCaseInvalidToolID, protocolProbeCaseToolIDCollision:
		toolIDs := []string{"call_probe"}
		if options.protocolProbeCase == protocolProbeCaseInvalidToolID {
			toolIDs = []string{"call.id:invalid"}
		} else if options.protocolProbeCase == protocolProbeCaseToolIDCollision {
			toolIDs = []string{"call.a", "call:a"}
		}
		toolCalls, err := common.Marshal(openAIToolProbeCalls(toolIDs))
		if err != nil {
			return nil, err
		}
		request.Messages = []dto.Message{
			{Role: "user", Content: "Use the supplied result."},
			{Role: "assistant", Content: nil, ToolCalls: toolCalls},
		}
		for _, toolID := range toolIDs {
			request.Messages = append(request.Messages, dto.Message{Role: "tool", ToolCallId: toolID, Content: "ok"})
		}
		request.Messages = append(request.Messages, dto.Message{Role: "user", Content: "Reply briefly."})
		request.Tools = []dto.ToolCallRequest{{
			Type: "function",
			Function: dto.FunctionRequest{
				Name: "probe_tool",
				Parameters: map[string]any{
					"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}},
				},
			},
		}}
	}
	return request, nil
}

// openAIToolProbeCalls returns the assistant tool-call items for a valid native chat history.
func openAIToolProbeCalls(toolIDs []string) []map[string]any {
	calls := make([]map[string]any, 0, len(toolIDs))
	for _, toolID := range toolIDs {
		calls = append(calls, map[string]any{
			"id": toolID, "type": "function",
			"function": map[string]any{"name": "probe_tool", "arguments": `{"value":"ok"}`},
		})
	}
	return calls
}

// buildGeminiProtocolProbeRequest creates valid native Gemini histories for each semantic probe label.
func buildGeminiProtocolProbeRequest(options channelTestOptions) (*dto.GeminiChatRequest, error) {
	request := &dto.GeminiChatRequest{
		Contents:         []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "Reply briefly."}}}},
		GenerationConfig: dto.GeminiChatGenerationConfig{MaxOutputTokens: lo.ToPtr(protocolProbeMaxTokens(options))},
	}
	switch options.protocolProbeCase {
	case protocolProbeCaseAssistantHistory, protocolProbeCaseReasoningHistory:
		request.Contents = []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "Continue the history."}}},
			{Role: "model", Parts: []dto.GeminiPart{{Text: "Ready."}}},
			{Role: "user", Parts: []dto.GeminiPart{{Text: "Reply briefly."}}},
		}
	case protocolProbeCaseToolCycle, protocolProbeCaseInvalidToolID, protocolProbeCaseToolIDCollision:
		request.Contents = []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "Use the supplied result."}}},
			{Role: "model", Parts: []dto.GeminiPart{{FunctionCall: &dto.FunctionCall{FunctionName: "probe_tool", Arguments: map[string]any{"value": "ok"}}}}},
			{Role: "user", Parts: []dto.GeminiPart{{FunctionResponse: &dto.GeminiFunctionResponse{Name: "probe_tool", Response: map[string]interface{}{"result": "ok"}}}, {Text: "Reply briefly."}}},
		}
		tools, err := common.Marshal([]dto.GeminiChatTool{{FunctionDeclarations: []map[string]any{{
			"name":       "probe_tool",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
		}}}})
		if err != nil {
			return nil, err
		}
		request.Tools = tools
	}
	return request, nil
}

// protocolProbeMaxTokens applies the existing small probe limit unless the caller supplied a lower-level override.
func protocolProbeMaxTokens(options channelTestOptions) uint {
	if options.maxOutputTokens > 0 {
		return options.maxOutputTokens
	}
	if options.protocolProbeCase == protocolProbeCaseBasic {
		return 16
	}
	return 32
}
