package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatRequestToClaudeMessagesPreservesNonLatestReasoningBeforeTextAndTools(t *testing.T) {
	firstReasoning := "first reasoning"
	secondReasoning := "second reasoning"
	first := dto.Message{Role: "assistant", Content: "visible", ReasoningContent: &firstReasoning}
	first.SetToolCalls([]dto.ToolCallRequest{{
		ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{"q":"x"}`},
	}})
	second := dto.Message{Role: "assistant", Content: "continued", ReasoningContent: &secondReasoning}

	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			first,
			{Role: "tool", ToolCallId: "call_1", Content: "result"},
			second,
			{Role: "user", Content: "next"},
		},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	require.Len(t, converted.Messages, 5)

	blocks, err := converted.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 3)
	assert.Equal(t, "thinking", blocks[0].Type)
	assert.Equal(t, "first reasoning", *blocks[0].Thinking)
	assert.Nil(t, blocks[0].Signature)
	assert.Equal(t, "text", blocks[1].Type)
	assert.Equal(t, "visible", blocks[1].GetText())
	assert.Equal(t, "tool_use", blocks[2].Type)

	secondBlocks, err := converted.Messages[3].ParseContent()
	require.NoError(t, err)
	require.Len(t, secondBlocks, 2)
	assert.Equal(t, "thinking", secondBlocks[0].Type)
	assert.Equal(t, "second reasoning", *secondBlocks[0].Thinking)
	assert.Equal(t, "continued", secondBlocks[1].GetText())
}

func TestOpenAIChatRequestToClaudeMessagesMergesConsecutiveAssistantReasoning(t *testing.T) {
	firstReasoning := "first "
	secondReasoning := "second"
	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: "visible", ReasoningContent: &firstReasoning},
			{Role: "assistant", Content: "continued", ReasoningContent: &secondReasoning},
			{Role: "user", Content: "next"},
		},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	require.Len(t, converted.Messages, 3)

	blocks, err := converted.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, "first second", *blocks[0].Thinking)
	assert.Equal(t, "visible continued", blocks[1].GetText())
}

func TestOpenAIChatRequestToClaudeMessagesMergesConsecutiveAssistantToolCallsInOrder(t *testing.T) {
	firstReasoning := "first "
	secondReasoning := "second"
	first := dto.Message{Role: "assistant", ReasoningContent: &firstReasoning}
	first.SetToolCalls([]dto.ToolCallRequest{{
		ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "first", Arguments: `{}`},
	}})
	second := dto.Message{Role: "assistant", Content: "visible", ReasoningContent: &secondReasoning}
	second.SetToolCalls([]dto.ToolCallRequest{{
		ID: "call_2", Type: "function", Function: dto.FunctionRequest{Name: "second", Arguments: `{}`},
	}})

	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			first,
			second,
			{Role: "tool", ToolCallId: "call_1", Content: "first result"},
			{Role: "tool", ToolCallId: "call_2", Content: "second result"},
			{Role: "assistant", Content: "done"},
			{Role: "user", Content: "next"},
		},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)

	blocks, err := converted.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 4)
	assert.Equal(t, "first second", *blocks[0].Thinking)
	assert.Equal(t, "visible", blocks[1].GetText())
	assert.Equal(t, "call_1", blocks[2].Id)
	assert.Equal(t, "call_2", blocks[3].Id)
}

func TestOpenAIChatRequestToClaudeMessagesWithholdsLatestUnsignedToolReasoning(t *testing.T) {
	reasoning := "unsigned reasoning"
	assistant := dto.Message{Role: "assistant", Content: "visible", ReasoningContent: &reasoning}
	assistant.SetToolCalls([]dto.ToolCallRequest{{
		ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{}`},
	}})
	info := &relaycommon.RelayInfo{}

	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			assistant,
			{Role: "tool", ToolCallId: "call_1", Content: "result"},
		},
	}, info)
	require.NoError(t, err)
	require.Len(t, converted.Messages, 3)

	blocks, err := converted.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "tool_use", blocks[1].Type)
	require.NotNil(t, info.ReasoningHistory)
	assert.Equal(t, 1, info.ReasoningHistory.UnsignedLatestTurnWithheld)
}

func TestOpenAIChatRequestToClaudeMessagesWithheldReasoningKeepsEmptyContentPlaceholder(t *testing.T) {
	reasoning := "unsigned reasoning"
	assistant := dto.Message{Role: "assistant", ReasoningContent: &reasoning}
	assistant.SetToolCalls([]dto.ToolCallRequest{{
		ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{}`},
	}})

	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			assistant,
			{Role: "tool", ToolCallId: "call_1", Content: "result"},
		},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)

	blocks, err := converted.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, "text", blocks[0].Type)
	assert.Equal(t, "...", blocks[0].GetText())
	assert.Equal(t, "tool_use", blocks[1].Type)
}

func TestOpenAIChatRequestToClaudeMessagesIgnoresReasoningOnUserMessage(t *testing.T) {
	reasoning := "not assistant reasoning"
	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "visible", ReasoningContent: &reasoning},
		},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	require.Len(t, converted.Messages, 1)
	assert.True(t, converted.Messages[0].IsStringContent())
	assert.Equal(t, "visible", converted.Messages[0].GetStringContent())
}

func TestOpenAIChatRequestToClaudeMessagesReasoningOnlyNeedsNoPlaceholder(t *testing.T) {
	reasoning := "history"
	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			{Role: "assistant", ReasoningContent: &reasoning},
			{Role: "user", Content: "next"},
		},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)

	blocks, err := converted.Messages[1].ParseContent()
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "thinking", blocks[0].Type)
}

func TestOpenAIChatRequestToClaudeMessagesWithoutReasoningKeepsStringShape(t *testing.T) {
	converted, err := OpenAIChatRequestToClaudeMessages(nil, dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: "answer"},
		},
	}, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	require.Len(t, converted.Messages, 2)
	assert.True(t, converted.Messages[0].IsStringContent())
	assert.True(t, converted.Messages[1].IsStringContent())
	assert.Equal(t, "question", converted.Messages[0].GetStringContent())
	assert.Equal(t, "answer", converted.Messages[1].GetStringContent())
}
