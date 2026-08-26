package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeMessagesRequestToOpenAIChatPreservesReasoningTextToolsAndResult(t *testing.T) {
	request := dto.ClaudeRequest{
		Model: "model-test",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{
				{Type: "thinking", Thinking: common.GetPointer("first ")},
				{Type: "text", Text: common.GetPointer("visible")},
				{Type: "thinking", Thinking: common.GetPointer("second")},
				{Type: "tool_use", Id: "call_1", Name: "lookup", Input: map[string]any{"q": "x"}},
			}},
			{Role: "user", Content: []dto.ClaudeMediaMessage{
				{Type: "tool_result", ToolUseId: "call_1", Content: "result"},
			}},
		},
	}
	info := &relaycommon.RelayInfo{}

	converted, err := ClaudeMessagesRequestToOpenAIChat(request, info)
	require.NoError(t, err)
	require.Len(t, converted.Messages, 3)

	assistant := converted.Messages[1]
	assert.Equal(t, "assistant", assistant.Role)
	assert.Equal(t, "first second", assistant.GetReasoningContent())
	require.Len(t, assistant.ParseContent(), 1)
	assert.Equal(t, "visible", assistant.ParseContent()[0].Text)
	toolCalls := assistant.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_1", toolCalls[0].ID)
	assert.Equal(t, "lookup", toolCalls[0].Function.Name)
	assert.JSONEq(t, `{"q":"x"}`, toolCalls[0].Function.Arguments)

	toolResult := converted.Messages[2]
	assert.Equal(t, "tool", toolResult.Role)
	assert.Equal(t, "call_1", toolResult.ToolCallId)
	assert.Equal(t, "result", toolResult.StringContent())
	require.NotNil(t, info.ReasoningHistory)
	assert.Equal(t, 1, info.ReasoningHistory.PreservedMessages)
}

func TestClaudeMessagesRequestToOpenAIChatPreservesReasoningOnlyAssistant(t *testing.T) {
	converted, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
		Model: "model-test",
		Messages: []dto.ClaudeMessage{{
			Role: "assistant",
			Content: []dto.ClaudeMediaMessage{{
				Type: "thinking", Thinking: common.GetPointer("reasoning only"),
			}},
		}},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	require.Len(t, converted.Messages, 1)
	assert.Equal(t, "reasoning only", converted.Messages[0].GetReasoningContent())
	assert.Nil(t, converted.Messages[0].Content)
}

func TestClaudeMessagesRequestToOpenAIChatSkipsOpaqueAndNonAssistantThinking(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	converted, err := ClaudeMessagesRequestToOpenAIChat(dto.ClaudeRequest{
		Model: "model-test",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "thinking", Thinking: common.GetPointer("not assistant")}}},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "redacted_thinking", Data: "opaque"}}},
		},
	}, info)
	require.NoError(t, err)
	assert.Empty(t, converted.Messages)
	require.NotNil(t, info.ReasoningHistory)
	assert.Equal(t, 1, info.ReasoningHistory.OpaqueBlocksSkipped)
}

func TestClaudeMediaMessageRedactedThinkingRoundTrip(t *testing.T) {
	emptySignature := ""
	original := dto.ClaudeMediaMessage{
		Type:      "redacted_thinking",
		Data:      "opaque-data",
		Signature: &emptySignature,
	}
	raw, err := common.Marshal(original)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"signature":""`)

	var decoded dto.ClaudeMediaMessage
	require.NoError(t, common.Unmarshal(raw, &decoded))
	assert.Equal(t, original.Data, decoded.Data)
	require.NotNil(t, decoded.Signature)
	assert.Empty(t, *decoded.Signature)

	nativeSignature := "native-signature"
	nativeRaw, err := common.Marshal(dto.ClaudeMediaMessage{
		Type:      "thinking",
		Thinking:  common.GetPointer("reasoning"),
		Signature: &nativeSignature,
	})
	require.NoError(t, err)
	var nativeDecoded dto.ClaudeMediaMessage
	require.NoError(t, common.Unmarshal(nativeRaw, &nativeDecoded))
	require.NotNil(t, nativeDecoded.Signature)
	assert.Equal(t, nativeSignature, *nativeDecoded.Signature)
}
