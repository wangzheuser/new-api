package relay

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContextPolicySelectionAudit distinguishes missing rules, explicit off and mapped logical models.
func TestContextPolicySelectionAudit(t *testing.T) {
	require.NoError(t, model_setting.SetContextTruncation(`{"models":{"MODEL_X":{"mode":"custom","window_tokens":2048,"output_reserve_tokens":64}}}`))
	t.Cleanup(func() { require.NoError(t, model_setting.SetContextTruncation(`{"models":{}}`)) })
	for _, tc := range []struct {
		name, model, mode, source, reason string
		enabled                           bool
	}{
		{"missing", "OTHER_MODEL", "", "disabled", "rule_not_matched", false},
		{"global", "MODEL_X", "", "global", "", true},
		{"inherit", "MODEL_X", "inherit", "global", "", true},
		{"channel-off", "MODEL_X", "off", "channel", "rule_disabled", false},
		{"channel-custom", "MODEL_X", "custom", "channel", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			c.Set("model_mapping", `{"MODEL_X":"upstream-model"}`)
			info := convertedResponsesViaChatTestInfo("http://127.0.0.1", false)
			info.RelayFormat = types.RelayFormatOpenAI
			info.SetAttemptModelName(tc.model)
			info.ChannelOtherSettings.ContextTruncation = &dto.ContextTruncationPolicy{Models: map[string]dto.ContextTruncationRule{tc.model: {Mode: tc.mode, WindowTokens: 1024, OutputReserveTokens: common.GetPointer(64)}}}
			request := &dto.GeneralOpenAIRequest{Model: tc.model, MaxTokens: common.GetPointer(uint(64)), Messages: []dto.Message{{Role: "user", Content: "hello"}}}
			require.NoError(t, helper.ModelMappedHelper(c, info, request))
			require.Nil(t, prepareTextInputPolicies(c, info, request, false))
			state := info.ContextTruncation
			require.NotNil(t, state)
			assert.Equal(t, tc.enabled, state.Enabled)
			assert.Equal(t, tc.source, state.Source)
			assert.Equal(t, tc.reason, state.Reason)
			assert.Equal(t, tc.model, state.Model)
			if tc.enabled {
				assert.Positive(t, state.Before)
				assert.Equal(t, state.Before, state.After)
				assert.Positive(t, state.Budget)
			}
			other := map[string]interface{}{}
			service.AppendInputPolicyLog(other, info)
			raw, err := common.Marshal(other)
			require.NoError(t, err)
			assert.Contains(t, string(raw), `"enabled":`)
			assert.Contains(t, string(raw), tc.model)
			assert.NotContains(t, string(raw), "hello")
		})
	}
}

// TestContextImageTokenLocations prevents nested images and Gemini file references from being omitted.
func TestContextImageTokenLocations(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		request    dto.Request
		images     int
	}{
		{"claude-tool", `{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/image.png"}}]}]}]}`, &dto.ClaudeRequest{}, 1},
		{"gemini-file-and-tool", `{"contents":[{"parts":[{"fileData":{"mimeType":"image/png","fileUri":"https://example.invalid/image.png"}},{"functionResponse":{"parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]}}]}]}`, &dto.GeminiChatRequest{}, 2},
		{"gemini-inline", `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]}]}`, &dto.GeminiChatRequest{}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, common.Unmarshal([]byte(tc.body), tc.request))
			meta, err := contextImageTokenMeta(tc.request)
			require.NoError(t, err)
			require.Len(t, meta.Files, tc.images)
			for _, file := range meta.Files {
				assert.Equal(t, types.FileTypeImage, file.FileType)
				assert.NotNil(t, file.Source)
			}
			again, err := contextImageTokenMeta(tc.request)
			require.NoError(t, err)
			assert.Len(t, again.Files, tc.images)
		})
	}
}
