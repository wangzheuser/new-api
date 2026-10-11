package claudemessages

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseClaude2OpenAIPreservesAllReasoningTextAndTools(t *testing.T) {
	response := ResponseClaude2OpenAI(&dto.ClaudeResponse{
		Id:         "msg_1",
		Model:      "model-test",
		StopReason: "tool_use",
		Content: []dto.ClaudeMediaMessage{
			{Type: "thinking", Thinking: common.GetPointer("first ")},
			{Type: "text", Text: common.GetPointer("visible ")},
			{Type: "thinking", Thinking: common.GetPointer("second")},
			{Type: "text", Text: common.GetPointer("answer")},
			{Type: "tool_use", Id: "call_1", Name: "lookup", Input: map[string]any{"q": "x"}},
		},
	})

	require.Len(t, response.Choices, 1)
	choice := response.Choices[0]
	assert.Equal(t, "first second", choice.Message.GetReasoningContent())
	assert.Equal(t, "visible answer", choice.Message.StringContent())
	toolCalls := choice.Message.ParseToolCalls()
	require.Len(t, toolCalls, 1)
	assert.Equal(t, "call_1", toolCalls[0].ID)
}

func TestStreamResponseClaude2OpenAIIgnoresSignatureDelta(t *testing.T) {
	response := StreamResponseClaude2OpenAI(&dto.ClaudeResponse{
		Type:  "content_block_delta",
		Index: common.GetPointer(0),
		Delta: &dto.ClaudeMediaMessage{
			Type:      "signature_delta",
			Signature: common.GetPointer("opaque-signature"),
		},
	})

	require.NotNil(t, response)
	require.Len(t, response.Choices, 1)
	assert.Nil(t, response.Choices[0].Delta.ReasoningContent)
	assert.Nil(t, response.Choices[0].Delta.Content)
}
