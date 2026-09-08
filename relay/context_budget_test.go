package relay

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContextBudgetHiddenHistory reproduces the no-trim bug without production conversation data.
func TestContextBudgetHiddenHistory(t *testing.T) {
	for _, protected := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		info := convertedResponsesViaChatTestInfo("http://127.0.0.1", true)
		info.RelayFormat = types.RelayFormatOpenAI
		info.ChannelOtherSettings.ContextTruncation = &dto.ContextTruncationPolicy{Models: map[string]dto.ContextTruncationRule{"MODEL_X": {Mode: "custom", WindowTokens: 2048}}}
		request := &dto.GeneralOpenAIRequest{Model: "MODEL_X", MaxTokens: common.GetPointer(uint(64)), Messages: []dto.Message{
			{Role: "user", Content: "old question"},
			{Role: "assistant", ReasoningContent: common.GetPointer(strings.Repeat("longReasoningValue", 4000))},
			{Role: "tool", ToolCallId: "call_old", Content: "done"},
		}}
		request.Messages[1].SetToolCalls([]any{map[string]any{"id": "call_old", "type": "function", "function": map[string]any{"name": "edit", "arguments": `{"code":"` + strings.Repeat("functionSignature", 6000) + `"}`}}})
		if !protected {
			request.Messages = append(request.Messages, dto.Message{Role: "user", Content: []any{map[string]any{"type": "text", "text": "recent"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,aGVsbG8="}}}})
		}
		before, err := common.Marshal(request)
		require.NoError(t, err)
		apiErr := prepareTextInputPolicies(c, info, request, false)
		if protected {
			require.NotNil(t, apiErr)
			after, err := common.Marshal(request)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			continue
		}
		require.Nil(t, apiErr)
		require.True(t, info.ContextTruncation.Applied)
		assert.Less(t, info.ContextTruncation.Before, info.ContextTruncation.Budget)
		assert.Greater(t, info.ContextTruncation.BudgetBefore, info.ContextTruncation.Budget)
		assert.LessOrEqual(t, info.ContextTruncation.BudgetAfter, info.ContextTruncation.Budget)
		require.Len(t, request.Messages, 1)
		recent, err := common.Marshal(request.Messages[0])
		require.NoError(t, err)
		assert.Contains(t, string(recent), "image_url")
		assert.Equal(t, info.ContextTruncation.After, c.GetInt(string(constant.ContextKeyPromptTokens)))
		// Final conversion cannot reintroduce uncounted thinking after the initial trim.
		info.RequestConversionChain = []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude}
		final, err := common.Marshal(map[string]any{"max_tokens": 64, "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": strings.Repeat("longReasoningValue", 4000)}}}}})
		require.NoError(t, err)
		require.ErrorContains(t, info.ValidateInputPolicyBody(final), "final_budget_exceeded")
	}
}

// TestContextBudgetMessageCompaction prevents old tool calls from moving to retained messages.
func TestContextBudgetMessageCompaction(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := convertedResponsesViaChatTestInfo("http://127.0.0.1", true)
	info.RelayFormat = types.RelayFormatOpenAI
	info.ChannelOtherSettings.ContextTruncation = &dto.ContextTruncationPolicy{Models: map[string]dto.ContextTruncationRule{"MODEL_X": {Mode: "custom", WindowTokens: 2048}}}
	request := &dto.GeneralOpenAIRequest{Model: "MODEL_X", MaxTokens: common.GetPointer(uint(64)), Messages: []dto.Message{
		{Role: "user", Content: strings.Repeat("old disposable context ", 5000)},
		{Role: "assistant", Content: "old answer", ReasoningContent: common.GetPointer("old thinking")},
		{Role: "tool", ToolCallId: "call_old", Content: "done"},
		{Role: "user", Content: "new question"},
		{Role: "assistant", Content: "new answer"},
	}}
	request.Messages[1].SetToolCalls([]any{map[string]any{"id": "call_old", "type": "function", "function": map[string]any{"name": "edit", "arguments": "{}"}}})
	require.Nil(t, prepareTextInputPolicies(c, info, request, false))
	require.Len(t, request.Messages, 2)
	assert.Empty(t, request.Messages[1].ToolCalls)
	assert.Nil(t, request.Messages[1].ReasoningContent)
	assert.Equal(t, "new answer", request.Messages[1].Content)
}

// TestContextBudgetFreshProtocolDTO ensures omitted protocol fields do not leak after shortening.
func TestContextBudgetFreshProtocolDTO(t *testing.T) {
	for _, tc := range []struct {
		request   dto.Request
		old, next string
	}{
		{&dto.ClaudeRequest{}, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"old","input":{"x":1}}]}]}`, `{"messages":[{"role":"assistant","content":[{"type":"text","text":"new"}]}]}`},
		{&dto.GeminiChatRequest{}, `{"contents":[{"parts":[{"functionCall":{"name":"old","args":{"x":1}}}]}]}`, `{"contents":[{"parts":[{"text":"new"}]}]}`},
		{&dto.OpenAIResponsesRequest{}, `{"input":[{"type":"function_call","call_id":"old","arguments":"{}"}]}`, `{"input":[{"role":"user","content":"new"}]}`},
	} {
		require.NoError(t, common.Unmarshal([]byte(tc.old), tc.request))
		require.NoError(t, decodeContextRequest(tc.request, []byte(tc.next)))
		encoded, err := common.Marshal(tc.request)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "old")
		assert.Contains(t, string(encoded), "new")
	}
}
