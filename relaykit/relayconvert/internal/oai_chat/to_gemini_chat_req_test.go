package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	sharedgemini "github.com/QuantumNous/new-api/relaykit/relayconvert/internal/shared/gemini"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatRequestToGeminiGenerateContentPlacesReasoningBeforeFunctionCall(t *testing.T) {
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
	}, &convmeta.Values{Options: &convmeta.Options{Gemini: convmeta.GeminiOptions{FunctionCallThoughtSignatureEnabled: true}}})
	require.NoError(t, err)
	require.Len(t, got.Contents, 2)
	parts := got.Contents[1].Parts
	require.Len(t, parts, 3)
	assert.True(t, parts[0].Thought)
	assert.Equal(t, reasoning, parts[0].Text)
	assert.Empty(t, parts[0].ThoughtSignature)
	require.NotNil(t, parts[1].FunctionCall)
	var signature string
	require.NoError(t, kitutil.Unmarshal(parts[1].ThoughtSignature, &signature))
	assert.Equal(t, sharedgemini.ThoughtSignatureBypassValue, signature)
	assert.Equal(t, "visible", parts[2].Text)
}
