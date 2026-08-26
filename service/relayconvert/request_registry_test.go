package relayconvert

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	sharedgemini "github.com/QuantumNous/new-api/service/relayconvert/internal/shared/gemini"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRequestConverterRegistryListsSupportedTextConverters(t *testing.T) {
	tests := []struct {
		converter      string
		from           types.RelayFormat
		to             types.RelayFormat
		quality        RequestConverterQuality
		stepConverters []string
		advancedCustom bool
	}{
		{converter: ConverterClaudeMessagesToOpenAIChat, from: types.RelayFormatClaude, to: types.RelayFormatOpenAI, quality: RequestConverterQualityFair, advancedCustom: true},
		{converter: ConverterGeminiContentToOpenAIChat, from: types.RelayFormatGemini, to: types.RelayFormatOpenAI, quality: RequestConverterQualityFair, advancedCustom: true},
		{converter: ConverterOpenAIChatToClaudeMessages, from: types.RelayFormatOpenAI, to: types.RelayFormatClaude, quality: RequestConverterQualityFair, advancedCustom: true},
		{converter: ConverterOpenAIChatToGeminiContent, from: types.RelayFormatOpenAI, to: types.RelayFormatGemini, quality: RequestConverterQualityFair, advancedCustom: true},
		{converter: ConverterOpenAIChatToOpenAIResponses, from: types.RelayFormatOpenAI, to: types.RelayFormatOpenAIResponses, quality: RequestConverterQualityGood, advancedCustom: true},
		{converter: ConverterOpenAIResponsesToOpenAIChat, from: types.RelayFormatOpenAIResponses, to: types.RelayFormatOpenAI, quality: RequestConverterQualityGood, advancedCustom: true},
		{
			converter: requestConverterClaudeToGemini,
			from:      types.RelayFormatClaude,
			to:        types.RelayFormatGemini,
			quality:   RequestConverterQualityDiscouraged,
			stepConverters: []string{
				ConverterClaudeMessagesToOpenAIChat,
				ConverterOpenAIChatToGeminiContent,
			},
		},
		{
			converter: requestConverterClaudeToResponses,
			from:      types.RelayFormatClaude,
			to:        types.RelayFormatOpenAIResponses,
			quality:   RequestConverterQualityFair,
			stepConverters: []string{
				ConverterClaudeMessagesToOpenAIChat,
				ConverterOpenAIChatToOpenAIResponses,
			},
		},
		{
			converter: requestConverterGeminiToClaude,
			from:      types.RelayFormatGemini,
			to:        types.RelayFormatClaude,
			quality:   RequestConverterQualityDiscouraged,
			stepConverters: []string{
				ConverterGeminiContentToOpenAIChat,
				ConverterOpenAIChatToClaudeMessages,
			},
		},
		{
			converter: requestConverterGeminiToResponses,
			from:      types.RelayFormatGemini,
			to:        types.RelayFormatOpenAIResponses,
			quality:   RequestConverterQualityFair,
			stepConverters: []string{
				ConverterGeminiContentToOpenAIChat,
				ConverterOpenAIChatToOpenAIResponses,
			},
		},
		{
			converter: requestConverterResponsesToClaude,
			from:      types.RelayFormatOpenAIResponses,
			to:        types.RelayFormatClaude,
			quality:   RequestConverterQualityFair,
		},
		{
			converter:      ConverterOpenAIResponsesToGemini,
			from:           types.RelayFormatOpenAIResponses,
			to:             types.RelayFormatGemini,
			quality:        RequestConverterQualityFair,
			advancedCustom: true,
		},
	}

	require.Len(t, requestConverters, len(tests))

	for _, tt := range tests {
		t.Run(tt.converter, func(t *testing.T) {
			spec, ok := LookupRequestConverter(tt.converter)

			require.True(t, ok)
			assert.Equal(t, tt.converter, spec.ID)
			assert.Equal(t, tt.from, spec.From)
			assert.Equal(t, tt.to, spec.To)
			assert.Equal(t, tt.quality, spec.Quality)
			assert.Equal(t, tt.stepConverters, spec.StepConverters)
			if len(tt.stepConverters) == 0 {
				assert.NotNil(t, spec.Convert)
			} else {
				assert.Nil(t, spec.Convert)
			}
			assert.Equal(t, tt.advancedCustom, dto.IsAdvancedCustomConverterAllowed(tt.converter))
		})
	}
}

func TestConvertRequestToTargetRecordsConversionChain(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI},
	}
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAIResponses, req)

	require.NoError(t, err)
	require.IsType(t, &dto.OpenAIResponsesRequest{}, result.Value)
	assert.Equal(t, types.RelayFormatOpenAI, result.From)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), result.To)
	assert.Equal(t, ConverterOpenAIChatToOpenAIResponses, result.Converter)
	assert.Equal(t, RequestConverterQualityGood, result.Quality)
	assert.Equal(t, []RequestStep{
		{
			Converter: ConverterOpenAIChatToOpenAIResponses,
			From:      types.RelayFormatOpenAI,
			To:        types.RelayFormatOpenAIResponses,
		},
	}, result.Steps)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}, info.RequestConversionChain)
}

func TestConvertRequestPlansMultiHopPath(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatClaude,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatClaude},
	}
	req := &dto.ClaudeRequest{
		Model: "claude-test",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAIResponses, req)

	require.NoError(t, err)
	require.IsType(t, &dto.OpenAIResponsesRequest{}, result.Value)
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), result.From)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), result.To)
	assert.Equal(t, requestConverterClaudeToResponses, result.Converter)
	assert.Equal(t, RequestConverterQualityFair, result.Quality)
	assert.Equal(t, []RequestStep{
		{
			Converter: ConverterClaudeMessagesToOpenAIChat,
			From:      types.RelayFormatClaude,
			To:        types.RelayFormatOpenAI,
		},
		{
			Converter: ConverterOpenAIChatToOpenAIResponses,
			From:      types.RelayFormatOpenAI,
			To:        types.RelayFormatOpenAIResponses,
		},
	}, result.Steps)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}, info.RequestConversionChain)
}

func TestReasoningHistoryRoundTripOpenAIResponseClaudeClientAndNextOpenAIRequest(t *testing.T) {
	reasoning := "inspect the tool input"
	message := dto.Message{Role: "assistant", Content: "calling lookup", ReasoningContent: &reasoning}
	message.SetToolCalls([]dto.ToolCallRequest{{
		ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`},
	}})

	claudeResponse := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id: "chatcmpl_1", Model: "model-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "tool_calls"}},
	}, &relaycommon.RelayInfo{})
	require.Len(t, claudeResponse.Content, 3)
	require.NotNil(t, claudeResponse.Content[0].Signature)
	assert.Empty(t, *claudeResponse.Content[0].Signature)

	nextRequest, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
		Model: "model-test",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: claudeResponse.Content},
			{Role: "user", Content: []dto.ClaudeMediaMessage{
				{Type: "tool_result", ToolUseId: "call_1", Content: "result"},
				{Type: "text", Text: common.GetPointer("continue")},
			}},
		},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	require.Len(t, nextRequest.Messages, 4)

	assistant := nextRequest.Messages[1]
	assert.Equal(t, reasoning, assistant.GetReasoningContent())
	require.Len(t, assistant.ParseContent(), 1)
	assert.Equal(t, "calling lookup", assistant.ParseContent()[0].Text)
	require.Len(t, assistant.ParseToolCalls(), 1)
	assert.Equal(t, "call_1", assistant.ParseToolCalls()[0].ID)
	assert.Equal(t, "tool", nextRequest.Messages[2].Role)
	assert.Equal(t, "call_1", nextRequest.Messages[2].ToolCallId)
	assert.Equal(t, "result", nextRequest.Messages[2].StringContent())
	assert.Equal(t, "user", nextRequest.Messages[3].Role)
	require.Len(t, nextRequest.Messages[3].ParseContent(), 1)
	assert.Equal(t, "continue", nextRequest.Messages[3].ParseContent()[0].Text)
}

func TestConvertRequestClaudeToResponsesPreservesReasoningBeforeFunctionCall(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatClaude,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatClaude},
	}
	request := claudeReasoningToolRequest()

	result, err := ConvertRequest(nil, info, types.RelayFormatOpenAIResponses, request)
	require.NoError(t, err)
	responsesRequest, ok := result.Value.(*dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, "reasoning", gjson.GetBytes(responsesRequest.Input, "1.type").String())
	assert.Equal(t, "reasoning history", gjson.GetBytes(responsesRequest.Input, "1.summary.0.text").String())
	assert.Equal(t, "assistant", gjson.GetBytes(responsesRequest.Input, "2.role").String())
	assert.Equal(t, "function_call", gjson.GetBytes(responsesRequest.Input, "3.type").String())
	assert.Equal(t, "function_call_output", gjson.GetBytes(responsesRequest.Input, "4.type").String())

	require.NotNil(t, info.ReasoningHistory)
	assert.Equal(t, 2, info.ReasoningHistory.PreservedMessages)
	require.Len(t, info.ReasoningHistory.Routes, 2)
}

func TestConvertRequestClaudeToGeminiPreservesThoughtBeforeFunctionCall(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	original := settings.FunctionCallThoughtSignatureEnabled
	settings.FunctionCallThoughtSignatureEnabled = true
	t.Cleanup(func() {
		settings.FunctionCallThoughtSignatureEnabled = original
	})
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatClaude,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatClaude},
		ChannelMeta:            &relaycommon.ChannelMeta{UpstreamModelName: "gemini-test"},
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatGemini, claudeReasoningToolRequest())
	require.NoError(t, err)
	geminiRequest, ok := result.Value.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(geminiRequest.Contents), 2)
	parts := geminiRequest.Contents[1].Parts
	require.GreaterOrEqual(t, len(parts), 2)
	assert.True(t, parts[0].Thought)
	assert.Equal(t, "reasoning history", parts[0].Text)
	assert.Empty(t, parts[0].ThoughtSignature)
	require.NotNil(t, parts[1].FunctionCall)
	assert.NotEmpty(t, parts[1].ThoughtSignature)
}

func TestReasoningHistoryRoundTripChatResponsesChatKeepsToolTurn(t *testing.T) {
	reasoning := "first second"
	assistant := dto.Message{Role: "assistant", Content: "visible", ReasoningContent: &reasoning}
	assistant.SetToolCalls([]dto.ToolCallRequest{{
		ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`},
	}})

	responsesRequest, err := ChatCompletionsRequestToResponsesRequest(&dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			assistant,
			{Role: "tool", ToolCallId: "call_1", Content: "result"},
		},
	})
	require.NoError(t, err)

	chatRequest, err := ResponsesRequestToChatCompletionsRequest(responsesRequest)
	require.NoError(t, err)
	require.Len(t, chatRequest.Messages, 3)
	assert.Equal(t, "user", chatRequest.Messages[0].Role)
	assert.Equal(t, "assistant", chatRequest.Messages[1].Role)
	assert.Equal(t, reasoning, chatRequest.Messages[1].GetReasoningContent())
	assert.Equal(t, "visible", chatRequest.Messages[1].StringContent())
	require.Len(t, chatRequest.Messages[1].ParseToolCalls(), 1)
	assert.Equal(t, "call_1", chatRequest.Messages[1].ParseToolCalls()[0].ID)
	assert.Equal(t, "tool", chatRequest.Messages[2].Role)
	assert.Equal(t, "call_1", chatRequest.Messages[2].ToolCallId)
}

func claudeReasoningToolRequest() *dto.ClaudeRequest {
	return &dto.ClaudeRequest{
		Model: "model-test",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{
				{Type: "thinking", Thinking: common.GetPointer("reasoning history")},
				{Type: "text", Text: common.GetPointer("visible")},
				{Type: "tool_use", Id: "call_1", Name: "lookup", Input: map[string]any{"q": "x"}},
			}},
			{Role: "user", Content: []dto.ClaudeMediaMessage{
				{Type: "tool_result", ToolUseId: "call_1", Content: "result"},
			}},
		},
	}
}

func TestConvertRequestViaExecutesExplicitPath(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI},
	}
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}

	result, err := ConvertRequestVia(nil, info, req, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses)

	require.NoError(t, err)
	require.IsType(t, &dto.OpenAIResponsesRequest{}, result.Value)
	assert.Equal(t, []RequestStep{
		{
			Converter: ConverterOpenAIChatToOpenAIResponses,
			From:      types.RelayFormatOpenAI,
			To:        types.RelayFormatOpenAIResponses,
		},
	}, result.Steps)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}, info.RequestConversionChain)
}

func TestConvertRequestResponsesToGeminiAppliesResponsesPreprocess(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAIResponses,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAIResponses},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-test",
		},
	}
	req := &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"role":    "user",
				"content": "next turn",
			},
			{
				"type":    "custom_tool_call",
				"call_id": "call_custom",
				"name":    "apply_patch",
				"input":   "patch body",
			},
			{
				"type":    "custom_tool_call_output",
				"call_id": "call_custom",
				"output":  "ok",
			},
			{
				"type":    "function_call_output",
				"call_id": "call_custom",
				"output":  "legacy custom output",
			},
		}),
		Tools: mustRawMessage(t, []map[string]any{
			{"type": "custom", "name": "apply_patch"},
		}),
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatGemini, req)

	require.NoError(t, err)
	geminiReq, ok := result.Value.(*dto.GeminiChatRequest)
	require.True(t, ok)
	assert.Empty(t, geminiReq.GetTools())
	require.Len(t, geminiReq.Contents, 1)
	assert.Equal(t, "user", geminiReq.Contents[0].Role)
	require.Len(t, geminiReq.Contents[0].Parts, 1)
	assert.Equal(t, "next turn", geminiReq.Contents[0].Parts[0].Text)
	assert.Equal(t, ConverterOpenAIResponsesToGemini, result.Converter)
	assert.Equal(t, RequestConverterQualityFair, result.Quality)
	assert.Equal(t, []RequestStep{
		{
			Converter: ConverterOpenAIResponsesToGemini,
			From:      types.RelayFormatOpenAIResponses,
			To:        types.RelayFormatGemini,
		},
	}, result.Steps)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatGemini}, info.RequestConversionChain)
}

func TestConvertRequestResponsesToGeminiUsesDirectConverter(t *testing.T) {
	geminiSettings := model_setting.GetGeminiSettings()
	originalThoughtSignatureEnabled := geminiSettings.FunctionCallThoughtSignatureEnabled
	geminiSettings.FunctionCallThoughtSignatureEnabled = true
	t.Cleanup(func() {
		geminiSettings.FunctionCallThoughtSignatureEnabled = originalThoughtSignatureEnabled
	})

	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAIResponses,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAIResponses},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-test",
		},
	}
	maxOutputTokens := uint(256)
	req := &dto.OpenAIResponsesRequest{
		Model:           "gemini-test",
		Instructions:    mustRawMessage(t, "system rules"),
		MaxOutputTokens: &maxOutputTokens,
		Input: mustRawMessage(t, []map[string]any{
			{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": "I will call."},
				},
			},
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "lookup",
				"arguments": map[string]any{"q": "x"},
			},
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output":  map[string]any{"ok": true},
			},
		}),
		Tools: mustRawMessage(t, []map[string]any{
			{
				"type":        "function",
				"name":        "lookup",
				"description": "Lookup data",
				"parameters": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"propertyNames":        map[string]any{"pattern": "^[a-z]+$"},
					"properties": map[string]any{
						"q": map[string]any{
							"type":             "string",
							"exclusiveMinimum": 0,
						},
						"filters": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": true,
								"properties": map[string]any{
									"name": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
			},
		}),
		Text: mustRawMessage(t, map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "answer",
				"schema": map[string]any{"type": "object"},
			},
		}),
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatGemini, req)

	require.NoError(t, err)
	geminiReq, ok := result.Value.(*dto.GeminiChatRequest)
	require.True(t, ok)
	assert.Equal(t, ConverterOpenAIResponsesToGemini, result.Converter)
	assert.Equal(t, []RequestStep{
		{
			Converter: ConverterOpenAIResponsesToGemini,
			From:      types.RelayFormatOpenAIResponses,
			To:        types.RelayFormatGemini,
		},
	}, result.Steps)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatGemini}, info.RequestConversionChain)

	require.NotNil(t, geminiReq.SystemInstructions)
	require.Len(t, geminiReq.SystemInstructions.Parts, 1)
	assert.Equal(t, "system rules", geminiReq.SystemInstructions.Parts[0].Text)
	assert.Equal(t, "application/json", geminiReq.GenerationConfig.ResponseMimeType)
	assert.Equal(t, maxOutputTokens, *geminiReq.GenerationConfig.MaxOutputTokens)

	tools := geminiReq.GetTools()
	require.Len(t, tools, 1)
	functions, err := common.Any2Type[[]dto.FunctionRequest](tools[0].FunctionDeclarations)
	require.NoError(t, err)
	require.Len(t, functions, 1)
	assert.Equal(t, "lookup", functions[0].Name)
	params, ok := functions[0].Parameters.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "OBJECT", params["type"])
	assert.NotContains(t, params, "additionalProperties")
	assert.NotContains(t, params, "propertyNames")
	properties, ok := params["properties"].(map[string]any)
	require.True(t, ok)
	queryParam, ok := properties["q"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "STRING", queryParam["type"])
	assert.NotContains(t, queryParam, "exclusiveMinimum")
	filterParam, ok := properties["filters"].(map[string]any)
	require.True(t, ok)
	filterItems, ok := filterParam["items"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, filterItems, "additionalProperties")

	require.Len(t, geminiReq.Contents, 2)
	assert.Equal(t, "model", geminiReq.Contents[0].Role)
	require.Len(t, geminiReq.Contents[0].Parts, 2)
	functionCall := geminiReq.Contents[0].Parts[0].FunctionCall
	require.NotNil(t, functionCall)
	assert.Equal(t, "lookup", functionCall.FunctionName)
	assert.Equal(t, map[string]any{"q": "x"}, functionCall.Arguments)
	var thoughtSignature string
	require.NoError(t, common.Unmarshal(geminiReq.Contents[0].Parts[0].ThoughtSignature, &thoughtSignature))
	assert.Equal(t, sharedgemini.ThoughtSignatureBypassValue, thoughtSignature)
	assert.Equal(t, "I will call.", geminiReq.Contents[0].Parts[1].Text)

	assert.Equal(t, "user", geminiReq.Contents[1].Role)
	require.Len(t, geminiReq.Contents[1].Parts, 1)
	functionResponse := geminiReq.Contents[1].Parts[0].FunctionResponse
	require.NotNil(t, functionResponse)
	assert.Equal(t, "lookup", functionResponse.Name)
	assert.Equal(t, true, functionResponse.Response["ok"])
	assert.Empty(t, geminiReq.Contents[1].Parts[0].ThoughtSignature)
}

func TestConvertRequestResponsesToGeminiSkipsThoughtSignatureWhenDisabled(t *testing.T) {
	geminiSettings := model_setting.GetGeminiSettings()
	originalThoughtSignatureEnabled := geminiSettings.FunctionCallThoughtSignatureEnabled
	geminiSettings.FunctionCallThoughtSignatureEnabled = false
	t.Cleanup(func() {
		geminiSettings.FunctionCallThoughtSignatureEnabled = originalThoughtSignatureEnabled
	})

	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAIResponses,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAIResponses},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-test",
		},
	}
	req := &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "lookup",
				"arguments": map[string]any{"q": "x"},
			},
		}),
		Tools: mustRawMessage(t, []map[string]any{
			{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}},
		}),
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatGemini, req)

	require.NoError(t, err)
	geminiReq, ok := result.Value.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Len(t, geminiReq.Contents, 1)
	require.Len(t, geminiReq.Contents[0].Parts, 1)
	require.NotNil(t, geminiReq.Contents[0].Parts[0].FunctionCall)
	assert.Empty(t, geminiReq.Contents[0].Parts[0].ThoughtSignature)
}

func TestConvertRequestOpenAIChatToGeminiAddsThoughtSignatureForAdvancedCustom(t *testing.T) {
	geminiSettings := model_setting.GetGeminiSettings()
	originalThoughtSignatureEnabled := geminiSettings.FunctionCallThoughtSignatureEnabled
	geminiSettings.FunctionCallThoughtSignatureEnabled = true
	t.Cleanup(func() {
		geminiSettings.FunctionCallThoughtSignatureEnabled = originalThoughtSignatureEnabled
	})

	assistantMessage := dto.Message{Role: "assistant", Content: ""}
	assistantMessage.SetToolCalls([]dto.ToolCallRequest{
		{
			ID:   "call_1",
			Type: "function",
			Function: dto.FunctionRequest{
				Name:      "lookup",
				Arguments: `{"q":"x"}`,
			},
		},
	})
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAdvancedCustom,
			UpstreamModelName: "gemini-test",
		},
	}
	req := &dto.GeneralOpenAIRequest{
		Model: "gemini-test",
		Messages: []dto.Message{
			{Role: "user", Content: "hi"},
			assistantMessage,
			{Role: "tool", ToolCallId: "call_1", Content: `{"ok":true}`},
		},
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:       "lookup",
					Parameters: map[string]any{"type": "object"},
				},
			},
		},
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatGemini, req)

	require.NoError(t, err)
	geminiReq, ok := result.Value.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Len(t, geminiReq.Contents, 3)
	assert.Equal(t, "model", geminiReq.Contents[1].Role)
	require.Len(t, geminiReq.Contents[1].Parts, 1)
	require.NotNil(t, geminiReq.Contents[1].Parts[0].FunctionCall)
	var thoughtSignature string
	require.NoError(t, common.Unmarshal(geminiReq.Contents[1].Parts[0].ThoughtSignature, &thoughtSignature))
	assert.Equal(t, sharedgemini.ThoughtSignatureBypassValue, thoughtSignature)
}

func TestConvertRequestResponsesToClaudeUsesDirectConverter(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAIResponses,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAIResponses},
	}
	stream := true
	parallelToolCalls := false
	maxOutputTokens := uint(512)
	req := &dto.OpenAIResponsesRequest{
		Model:             "claude-test",
		Instructions:      mustRawMessage(t, "system rules"),
		Stream:            &stream,
		MaxOutputTokens:   &maxOutputTokens,
		ParallelToolCalls: mustRawMessage(t, parallelToolCalls),
		Reasoning:         &dto.Reasoning{Effort: "medium"},
		Input: mustRawMessage(t, []map[string]any{
			{
				"role":    "user",
				"content": "question",
			},
			{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": "I will call."},
				},
			},
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "lookup",
				"arguments": map[string]any{"q": "x"},
			},
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output":  map[string]any{"ok": true},
			},
		}),
		Tools: mustRawMessage(t, []map[string]any{
			{
				"type":        "function",
				"name":        "lookup",
				"description": "Lookup data",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"q": map[string]any{"type": "string"},
					},
				},
			},
		}),
	}

	result, err := ConvertRequest(nil, info, types.RelayFormatClaude, req)

	require.NoError(t, err)
	claudeReq, ok := result.Value.(*dto.ClaudeRequest)
	require.True(t, ok)
	assert.Equal(t, requestConverterResponsesToClaude, result.Converter)
	assert.Equal(t, []RequestStep{
		{
			Converter: requestConverterResponsesToClaude,
			From:      types.RelayFormatOpenAIResponses,
			To:        types.RelayFormatClaude,
		},
	}, result.Steps)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatClaude}, info.RequestConversionChain)

	system, err := common.Any2Type[[]dto.ClaudeMediaMessage](claudeReq.System)
	require.NoError(t, err)
	require.Len(t, system, 1)
	assert.Equal(t, "system rules", system[0].GetText())
	require.NotNil(t, claudeReq.Stream)
	assert.True(t, *claudeReq.Stream)
	assert.Equal(t, maxOutputTokens, *claudeReq.MaxTokens)
	require.NotNil(t, claudeReq.Thinking)
	assert.Equal(t, "enabled", claudeReq.Thinking.Type)
	assert.Equal(t, 2048, claudeReq.Thinking.GetBudgetTokens())

	tools, err := common.Any2Type[[]*dto.Tool](claudeReq.Tools)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "lookup", tools[0].Name)

	require.Len(t, claudeReq.Messages, 3)
	assert.Equal(t, "user", claudeReq.Messages[0].Role)
	userParts, err := claudeReq.Messages[0].ParseContent()
	require.NoError(t, err)
	require.Len(t, userParts, 1)
	assert.Equal(t, "question", userParts[0].GetText())

	assert.Equal(t, "assistant", claudeReq.Messages[1].Role)
	assistantParts, err := claudeReq.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, assistantParts, 2)
	assert.Equal(t, "I will call.", assistantParts[0].GetText())
	assert.Equal(t, "tool_use", assistantParts[1].Type)
	assert.Equal(t, "call_1", assistantParts[1].Id)
	assert.Equal(t, "lookup", assistantParts[1].Name)
	assert.Equal(t, map[string]any{"q": "x"}, assistantParts[1].Input)

	assert.Equal(t, "user", claudeReq.Messages[2].Role)
	toolResultParts, err := claudeReq.Messages[2].ParseContent()
	require.NoError(t, err)
	require.Len(t, toolResultParts, 1)
	assert.Equal(t, "tool_result", toolResultParts[0].Type)
	assert.Equal(t, "call_1", toolResultParts[0].ToolUseId)
	assert.Equal(t, map[string]any{"ok": true}, toolResultParts[0].Content)
}

func TestConvertRequestViaResponsesToGeminiStillUsesDirectSteps(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAIResponses,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAIResponses},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-test",
		},
	}
	req := &dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustRawMessage(t, []map[string]any{
			{
				"role":    "user",
				"content": "hello",
			},
		}),
	}

	result, err := ConvertRequestVia(nil, info, req, types.RelayFormatOpenAI, types.RelayFormatGemini)

	require.NoError(t, err)
	require.IsType(t, &dto.GeminiChatRequest{}, result.Value)
	assert.Equal(t, ConverterOpenAIResponsesToOpenAIChat+","+ConverterOpenAIChatToGeminiContent, result.Converter)
	assert.Equal(t, []RequestStep{
		{
			Converter: ConverterOpenAIResponsesToOpenAIChat,
			From:      types.RelayFormatOpenAIResponses,
			To:        types.RelayFormatOpenAI,
		},
		{
			Converter: ConverterOpenAIChatToGeminiContent,
			From:      types.RelayFormatOpenAI,
			To:        types.RelayFormatGemini,
		},
	}, result.Steps)
}

func TestConvertRequestByIDDeduplicatesConversionChain(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses},
	}
	req := &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}

	result, err := ConvertRequestByID(nil, info, ConverterOpenAIChatToOpenAIResponses, req)

	require.NoError(t, err)
	require.IsType(t, &dto.OpenAIResponsesRequest{}, result.Value)
	require.Len(t, result.Steps, 1)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}, info.RequestConversionChain)
}

func TestConvertRequestByIDExecutesMultiHopConverter(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat:            types.RelayFormatClaude,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatClaude},
	}
	req := &dto.ClaudeRequest{
		Model: "claude-test",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}

	result, err := ConvertRequestByID(nil, info, requestConverterClaudeToResponses, req)

	require.NoError(t, err)
	require.IsType(t, &dto.OpenAIResponsesRequest{}, result.Value)
	assert.Equal(t, requestConverterClaudeToResponses, result.Converter)
	assert.Equal(t, RequestConverterQualityFair, result.Quality)
	assert.Equal(t, []RequestStep{
		{
			Converter: ConverterClaudeMessagesToOpenAIChat,
			From:      types.RelayFormatClaude,
			To:        types.RelayFormatOpenAI,
		},
		{
			Converter: ConverterOpenAIChatToOpenAIResponses,
			From:      types.RelayFormatOpenAI,
			To:        types.RelayFormatOpenAIResponses,
		},
	}, result.Steps)
	assert.Equal(t, []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}, info.RequestConversionChain)
}

func TestConvertRequestRejectsUnsupportedConverterAndNilRequest(t *testing.T) {
	_, err := ConvertRequestByID(nil, &relaycommon.RelayInfo{}, "missing_converter", &dto.GeneralOpenAIRequest{Model: "gpt-test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")

	_, err = ConvertRequest(nil, &relaycommon.RelayInfo{}, types.RelayFormatOpenAIResponses, (*dto.GeneralOpenAIRequest)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request is nil")
}

func TestConvertRequestByIDRejectsWrongSourceFormat(t *testing.T) {
	_, err := ConvertRequestByID(
		nil,
		&relaycommon.RelayInfo{},
		ConverterOpenAIChatToOpenAIResponses,
		&dto.ClaudeRequest{Model: "claude-test"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "expects openai request")
}

func TestConvertRequestRejectsUnregisteredExplicitPath(t *testing.T) {
	_, err := ConvertRequest(
		nil,
		&relaycommon.RelayInfo{},
		types.RelayFormatEmbedding,
		&dto.ClaudeRequest{Model: "claude-test"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "from claude to embedding is not registered")
}

func mustRawMessage(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	return raw
}
