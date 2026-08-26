package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseOpenAI2ClaudeToolUseInputIsObject(t *testing.T) {
	tests := []struct {
		name string
		args string
		want map[string]interface{}
	}{
		{name: "object", args: `{"q":"x"}`, want: map[string]interface{}{"q": "x"}},
		{name: "empty", args: "", want: map[string]interface{}{}},
		{name: "invalid", args: "{", want: map[string]interface{}{}},
		{name: "null", args: "null", want: map[string]interface{}{}},
		{name: "array", args: `["x"]`, want: map[string]interface{}{}},
		{name: "string", args: `"x"`, want: map[string]interface{}{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := dto.Message{Role: "assistant"}
			msg.SetToolCalls([]dto.ToolCallRequest{
				{
					ID:   "call_1",
					Type: "function",
					Function: dto.FunctionRequest{
						Name:      "lookup",
						Arguments: tt.args,
					},
				},
			})

			resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
				Id:    "chatcmpl_1",
				Model: "gpt-test",
				Choices: []dto.OpenAITextResponseChoice{
					{Message: msg, FinishReason: "tool_calls"},
				},
			}, nil)

			require.Len(t, resp.Content, 1)
			assert.Equal(t, "tool_use", resp.Content[0].Type)
			assert.Equal(t, tt.want, resp.Content[0].Input)
		})
	}
}

func TestResponseOpenAI2ClaudeUsageCarriesOpenAIBillingUsage(t *testing.T) {
	resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{Message: dto.Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"},
		},
		Usage: dto.Usage{
			PromptTokens:     11,
			CompletionTokens: 5,
			TotalTokens:      16,
		},
	}, nil)

	require.NotNil(t, resp.Usage)
	assert.Equal(t, 11, resp.Usage.InputTokens)
	assert.Equal(t, 5, resp.Usage.OutputTokens)
	require.NotNil(t, resp.Usage.BillingUsage)
	require.NotNil(t, resp.Usage.BillingUsage.OpenAIUsage)
	assert.Equal(t, dto.BillingUsageSourceOAIChat, resp.Usage.BillingUsage.Source)
	assert.Equal(t, dto.BillingUsageSemanticOpenAI, resp.Usage.BillingUsage.Semantic)
	assert.Equal(t, 11, resp.Usage.BillingUsage.OpenAIUsage.PromptTokens)
	assert.Equal(t, 5, resp.Usage.BillingUsage.OpenAIUsage.CompletionTokens)
	assert.Equal(t, 16, resp.Usage.BillingUsage.OpenAIUsage.TotalTokens)
	assert.Nil(t, resp.Usage.BillingUsage.OpenAIUsage.BillingUsage)
}

func TestResponseOpenAI2ClaudePreservesReasoningTextAndParallelTools(t *testing.T) {
	reasoning := "reasoning"
	message := dto.Message{Role: "assistant", Content: "visible", ReasoningContent: &reasoning}
	message.SetToolCalls([]dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`}},
		{ID: "call_2", Type: "function", Function: dto.FunctionRequest{Name: "lookup2", Arguments: `{}`}},
	})
	info := &relaycommon.RelayInfo{}

	resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id: "chatcmpl_1", Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "tool_calls"}},
	}, info)

	require.Len(t, resp.Content, 4)
	assert.Equal(t, "thinking", resp.Content[0].Type)
	assert.Equal(t, reasoning, *resp.Content[0].Thinking)
	require.NotNil(t, resp.Content[0].Signature)
	assert.Empty(t, *resp.Content[0].Signature)
	assert.Equal(t, "text", resp.Content[1].Type)
	assert.Equal(t, "visible", resp.Content[1].GetText())
	assert.Equal(t, "tool_use", resp.Content[2].Type)
	assert.Equal(t, "tool_use", resp.Content[3].Type)
	require.NotNil(t, info.ReasoningHistory)
	assert.Equal(t, 1, info.ReasoningHistory.SyntheticClientSignatures)
}

func TestResponseOpenAI2ClaudeReasoningOnlyHasNoEmptyText(t *testing.T) {
	reasoning := "reasoning only"
	resp := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id: "chatcmpl_1", Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{
			Message: dto.Message{Role: "assistant", ReasoningContent: &reasoning}, FinishReason: "stop",
		}},
	}, &relaycommon.RelayInfo{})

	require.Len(t, resp.Content, 1)
	assert.Equal(t, "thinking", resp.Content[0].Type)
}

func TestStreamAndNonStreamOpenAI2ClaudeProduceEquivalentContentBlocks(t *testing.T) {
	reasoning := "reasoning"
	message := dto.Message{Role: "assistant", Content: "visible", ReasoningContent: &reasoning}
	message.SetToolCalls([]dto.ToolCallRequest{
		{ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "first", Arguments: `{"q":"x"}`}},
		{ID: "call_2", Type: "function", Function: dto.FunctionRequest{Name: "second", Arguments: `{}`}},
	})
	nonStream := ResponseOpenAI2Claude(&dto.OpenAITextResponse{
		Id: "chatcmpl_1", Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{{Message: message, FinishReason: "tool_calls"}},
	}, &relaycommon.RelayInfo{})

	info := &relaycommon.RelayInfo{}
	streamEvents := make([]*dto.ClaudeResponse, 0)
	info.SendResponseCount = 1
	streamEvents = append(streamEvents, StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ReasoningContent: ptr(reasoning)},
		}},
	}, info)...)
	info.SendResponseCount = 2
	streamEvents = append(streamEvents, StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: ptr("visible")},
		}},
	}, info)...)
	info.SendResponseCount = 3
	streamEvents = append(streamEvents, StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: ptr(0), ID: "call_1", Type: "function", Function: dto.FunctionResponse{Name: "first", Arguments: `{"q":"x"}`}},
				{Index: ptr(1), ID: "call_2", Type: "function", Function: dto.FunctionResponse{Name: "second", Arguments: `{}`}},
			}},
		}},
	}, info)...)
	info.SendResponseCount = 4
	streamEvents = append(streamEvents, StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id: "chatcmpl_1", Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: ptr("tool_calls")}},
		Usage:   &dto.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}, info)...)

	streamContent := accumulateClaudeStreamContent(t, streamEvents)
	require.Len(t, streamContent, len(nonStream.Content))
	for i := range nonStream.Content {
		assert.Equal(t, nonStream.Content[i].Type, streamContent[i].Type)
		assert.Equal(t, nonStream.Content[i].GetText(), streamContent[i].GetText())
		assert.Equal(t, nonStream.Content[i].Thinking, streamContent[i].Thinking)
		assert.Equal(t, nonStream.Content[i].Signature, streamContent[i].Signature)
		assert.Equal(t, nonStream.Content[i].Id, streamContent[i].Id)
		assert.Equal(t, nonStream.Content[i].Name, streamContent[i].Name)
		assert.Equal(t, nonStream.Content[i].Input, streamContent[i].Input)
	}
}

func accumulateClaudeStreamContent(t *testing.T, events []*dto.ClaudeResponse) []dto.ClaudeMediaMessage {
	t.Helper()
	blocks := map[int]*dto.ClaudeMediaMessage{}
	partialJSON := map[int]string{}
	maxIndex := -1
	for _, event := range events {
		if event.Index == nil {
			continue
		}
		index := *event.Index
		if index > maxIndex {
			maxIndex = index
		}
		switch event.Type {
		case "content_block_start":
			block := *event.ContentBlock
			blocks[index] = &block
		case "content_block_delta":
			block := blocks[index]
			if block == nil || event.Delta == nil {
				continue
			}
			switch event.Delta.Type {
			case "thinking_delta":
				thinking := block.GetText()
				if block.Thinking != nil {
					thinking = *block.Thinking
				}
				thinking += *event.Delta.Thinking
				block.Thinking = &thinking
			case "text_delta":
				text := block.GetText() + event.Delta.GetText()
				block.Text = &text
			case "signature_delta":
				block.Signature = event.Delta.Signature
			case "input_json_delta":
				partialJSON[index] += *event.Delta.PartialJson
			}
		}
	}

	content := make([]dto.ClaudeMediaMessage, 0, maxIndex+1)
	for index := 0; index <= maxIndex; index++ {
		block := blocks[index]
		require.NotNil(t, block)
		if block.Type == "tool_use" {
			input := map[string]interface{}{}
			if partialJSON[index] != "" {
				require.NoError(t, common.Unmarshal([]byte(partialJSON[index]), &input))
			}
			block.Input = input
		}
		content = append(content, *block)
	}
	return content
}

func TestBuildClaudeUsageFromOpenAICacheWriteUsage(t *testing.T) {
	usage := buildClaudeUsageFromOpenAIUsage(&dto.Usage{
		PromptTokens:     3619,
		CompletionTokens: 36,
		TotalTokens:      3655,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:     2921,
			CacheWriteTokens: 3616,
		},
	})

	require.NotNil(t, usage)
	// Claude semantics reports input_tokens excluding cache read/write; the
	// overlapping unadjusted prefixes drive the remainder negative, clamp to 0.
	assert.Equal(t, 0, usage.InputTokens)
	assert.Equal(t, 2921, usage.CacheReadInputTokens)
	assert.Equal(t, 3616, usage.CacheCreationInputTokens)
	assert.Equal(t, 36, usage.OutputTokens)
	require.NotNil(t, usage.BillingUsage)
	require.NotNil(t, usage.BillingUsage.OpenAIUsage)
	assert.Equal(t, dto.BillingUsageSemanticOpenAI, usage.BillingUsage.Semantic)
	assert.Equal(t, 3616, usage.BillingUsage.OpenAIUsage.PromptTokensDetails.CacheWriteTokens)
}

func TestStreamResponseOpenAI2ClaudeClosesTextThinkingAndToolBlocks(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		},
	}

	info.SendResponseCount = 1
	textResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Content: ptr("hello"),
				},
			},
		},
	}, info)
	require.Len(t, textResponses, 3)
	assert.Equal(t, "message_start", textResponses[0].Type)
	assert.Equal(t, "content_block_start", textResponses[1].Type)
	assert.Equal(t, 0, textResponses[1].GetIndex())
	assert.Equal(t, "content_block_delta", textResponses[2].Type)

	info.SendResponseCount = 2
	thinkingResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ReasoningContent: ptr("thinking"),
				},
			},
		},
	}, info)
	require.Len(t, thinkingResponses, 3)
	assert.Equal(t, "content_block_stop", thinkingResponses[0].Type)
	assert.Equal(t, 0, thinkingResponses[0].GetIndex())
	assert.Equal(t, "content_block_start", thinkingResponses[1].Type)
	assert.Equal(t, 1, thinkingResponses[1].GetIndex())
	assert.Equal(t, "thinking", thinkingResponses[1].ContentBlock.Type)
	assert.Equal(t, "content_block_delta", thinkingResponses[2].Type)

	info.SendResponseCount = 3
	toolResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					ToolCalls: []dto.ToolCallResponse{
						{
							Index: ptr(0),
							ID:    "call_1",
							Type:  "function",
							Function: dto.FunctionResponse{
								Name:      "lookup",
								Arguments: `{"q":"x"}`,
							},
						},
					},
				},
			},
		},
	}, info)
	require.Len(t, toolResponses, 4)
	assert.Equal(t, "content_block_delta", toolResponses[0].Type)
	assert.Equal(t, "signature_delta", toolResponses[0].Delta.Type)
	require.NotNil(t, toolResponses[0].Delta.Signature)
	assert.Empty(t, *toolResponses[0].Delta.Signature)
	assert.Equal(t, "content_block_stop", toolResponses[1].Type)
	assert.Equal(t, 1, toolResponses[1].GetIndex())
	assert.Equal(t, "content_block_start", toolResponses[2].Type)
	assert.Equal(t, 2, toolResponses[2].GetIndex())
	assert.Equal(t, "tool_use", toolResponses[2].ContentBlock.Type)
	assert.Equal(t, "content_block_delta", toolResponses[3].Type)

	info.SendResponseCount = 4
	finishResponses := StreamResponseOpenAI2Claude(&dto.ChatCompletionsStreamResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{FinishReason: ptr("tool_calls")},
		},
		Usage: &dto.Usage{
			PromptTokens:     7,
			CompletionTokens: 3,
			TotalTokens:      10,
		},
	}, info)
	require.Len(t, finishResponses, 3)
	assert.Equal(t, "content_block_stop", finishResponses[0].Type)
	assert.Equal(t, 2, finishResponses[0].GetIndex())
	assert.Equal(t, "message_delta", finishResponses[1].Type)
	assert.Equal(t, "tool_use", *finishResponses[1].Delta.StopReason)
	require.NotNil(t, finishResponses[1].Usage)
	require.NotNil(t, finishResponses[1].Usage.BillingUsage)
	require.NotNil(t, finishResponses[1].Usage.BillingUsage.OpenAIUsage)
	assert.Equal(t, 7, finishResponses[1].Usage.BillingUsage.OpenAIUsage.PromptTokens)
	assert.Equal(t, 3, finishResponses[1].Usage.BillingUsage.OpenAIUsage.CompletionTokens)
	assert.Equal(t, "message_stop", finishResponses[2].Type)
}

func TestNormalizeCacheCreationSplit(t *testing.T) {
	cache5m, cache1h := NormalizeCacheCreationSplit(10, 3, 2)
	assert.Equal(t, 8, cache5m)
	assert.Equal(t, 2, cache1h)

	cache5m, cache1h = NormalizeCacheCreationSplit(3, 5, 1)
	assert.Equal(t, 5, cache5m)
	assert.Equal(t, 1, cache1h)
}

func ptr[T any](value T) *T {
	return &value
}
