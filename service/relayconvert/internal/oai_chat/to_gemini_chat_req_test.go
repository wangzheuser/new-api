package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	sharedgemini "github.com/QuantumNous/new-api/service/relayconvert/internal/shared/gemini"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatRequestToGeminiGenerateContentPlacesReasoningBeforeFunctionCall(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	original := settings.FunctionCallThoughtSignatureEnabled
	settings.FunctionCallThoughtSignatureEnabled = true
	t.Cleanup(func() {
		settings.FunctionCallThoughtSignatureEnabled = original
	})

	reasoning := "reasoning history"
	assistant := dto.Message{Role: "assistant", Content: "visible", ReasoningContent: &reasoning}
	assistant.SetToolCalls([]dto.ToolCallRequest{{
		ID: "call_1", Type: "function", Function: dto.FunctionRequest{Name: "lookup", Arguments: `{}`},
	}})

	got, err := OpenAIChatRequestToGeminiGenerateContent(nil, dto.GeneralOpenAIRequest{
		Model: "gemini-test",
		Messages: []dto.Message{
			{Role: "user", Content: "question"},
			assistant,
		},
	}, nil)
	require.NoError(t, err)
	require.Len(t, got.Contents, 2)
	parts := got.Contents[1].Parts
	require.Len(t, parts, 3)
	assert.True(t, parts[0].Thought)
	assert.Equal(t, reasoning, parts[0].Text)
	assert.Empty(t, parts[0].ThoughtSignature)
	require.NotNil(t, parts[1].FunctionCall)
	var signature string
	require.NoError(t, common.Unmarshal(parts[1].ThoughtSignature, &signature))
	assert.Equal(t, sharedgemini.ThoughtSignatureBypassValue, signature)
	assert.Equal(t, "visible", parts[2].Text)
}
