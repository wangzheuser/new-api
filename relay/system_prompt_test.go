package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	rootdto "github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	projecttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reserveCapture struct {
	target int
}

func (r *reserveCapture) Settle(int) error              { return nil }
func (r *reserveCapture) Refund(*gin.Context)           {}
func (r *reserveCapture) NeedsRefund() bool             { return false }
func (r *reserveCapture) GetPreConsumedQuota() int      { return 0 }
func (r *reserveCapture) Reserve(targetQuota int) error { r.target = targetQuota; return nil }

func TestApplySystemPromptAcrossRelayFormats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	openAIRequest := &kitdto.GeneralOpenAIRequest{
		Model: "model-a",
		Messages: []kitdto.Message{{
			Role:    "system",
			Content: "client prompt",
		}},
	}
	applyOpenAISystemPrompt(c, openAIRequest, "model prompt", true)
	assert.Equal(t, "model prompt\nclient prompt", openAIRequest.Messages[0].StringContent())

	responsesInstructions, err := common.Marshal("client prompt")
	require.NoError(t, err)
	responsesRequest := &kitdto.OpenAIResponsesRequest{Instructions: responsesInstructions}
	applied, err := applyResponsesSystemPrompt(c, responsesRequest, "model prompt", true)
	require.NoError(t, err)
	require.True(t, applied)
	var mergedInstructions string
	require.NoError(t, common.Unmarshal(responsesRequest.Instructions, &mergedInstructions))
	assert.Equal(t, "model prompt\nclient prompt", mergedInstructions)

	claudeRequest := &kitdto.ClaudeRequest{System: "client prompt"}
	applyClaudeSystemPrompt(c, claudeRequest, "model prompt", true)
	assert.Equal(t, "model prompt\nclient prompt", claudeRequest.GetStringSystem())

	geminiRequest := &kitdto.GeminiChatRequest{
		SystemInstructions: &kitdto.GeminiChatContent{
			Parts: []kitdto.GeminiPart{{Text: "client prompt"}},
		},
	}
	applyGeminiSystemPrompt(c, geminiRequest, "model prompt", true)
	assert.Equal(t, "model prompt\nclient prompt", geminiRequest.SystemInstructions.Parts[0].Text)
}

func TestResolveSystemPromptUsesImmutableRequestedModel(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RequestedModelName: "public-model",
		OriginModelName:    "mapped-model",
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: kitdto.ChannelSettings{
			ModelSystemPrompts: map[string]string{"public-model": "public prompt"},
		}},
	}

	prompt, prepend, _, _ := resolveSystemPrompt(info)

	assert.Equal(t, "public prompt", prompt)
	assert.True(t, prepend)
}

func TestApplySystemPromptForRequestUsesConfiguredModelPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	request := &kitdto.GeneralOpenAIRequest{
		Model:    "gpt-3.5-turbo",
		Messages: []kitdto.Message{{Role: "user", Content: "你好"}},
	}
	info := &relaycommon.RelayInfo{
		RequestedModelName: "gpt-3.5-turbo",
		OriginModelName:    "gpt-3.5-turbo",
		UsingGroup:         "default",
		UserGroup:          "default",
		RelayFormat:        projecttypes.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: kitdto.ChannelSettings{
			ModelSystemPrompts: map[string]string{"gpt-3.5-turbo": "model prompt"},
		}},
	}

	apiErr := ApplySystemPromptForRequest(c, info, request)

	require.Nil(t, apiErr)
	require.Len(t, request.Messages, 2)
	assert.Equal(t, "system", request.Messages[0].Role)
	assert.Equal(t, "model prompt", request.Messages[0].StringContent())
	assert.Equal(t, "你好", request.Messages[1].StringContent())
}

func TestInjectedSystemPromptUpdatesEstimateAndReserve(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-3.5-turbo")

	request := &kitdto.GeneralOpenAIRequest{
		Model:    "gpt-3.5-turbo",
		Messages: []kitdto.Message{{Role: "user", Content: "hello"}},
	}
	before := request.GetTokenCountMeta()
	applyOpenAISystemPrompt(c, request, strings.Repeat("model prompt ", 1000), true)
	billing := &reserveCapture{}
	info := &relaycommon.RelayInfo{
		RequestedModelName: "gpt-3.5-turbo",
		OriginModelName:    "gpt-3.5-turbo",
		UsingGroup:         "default",
		UserGroup:          "default",
		RelayFormat:        projecttypes.RelayFormatOpenAI,
		Billing:            billing,
		PriceData: projecttypes.PriceData{
			ModelRatio: 1,
			GroupRatioInfo: projecttypes.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: kitdto.ChannelSettings{
			ModelSystemPrompts: map[string]string{"gpt-3.5-turbo": "configured"},
		}},
	}
	info.SetEstimatePromptTokens(10)

	apiErr := accountInjectedSystemPrompt(c, info, before, request.GetTokenCountMeta())

	require.Nil(t, apiErr)
	assert.Greater(t, info.GetEstimatePromptTokens(), 10)
	assert.Greater(t, billing.target, 0)

	firstEstimate := info.GetEstimatePromptTokens()
	info.ResetEstimatePromptTokens()
	apiErr = accountInjectedSystemPrompt(c, info, before, request.GetTokenCountMeta())
	require.Nil(t, apiErr)
	assert.Equal(t, firstEstimate, info.GetEstimatePromptTokens())
}

func TestApplyChannelDefaultDoesNotReplaceClientPrompt(t *testing.T) {
	request := &kitdto.GeneralOpenAIRequest{
		Messages: []kitdto.Message{{Role: "system", Content: "client prompt"}},
	}

	applyOpenAISystemPrompt(nil, request, "channel default", false)

	assert.Equal(t, "client prompt", request.Messages[0].StringContent())
}

// TestSystemPromptResponsesWebSocket keeps response.create injection repeatable across session turns.
func TestSystemPromptResponsesWebSocket(t *testing.T) {
	oldPass := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() { model_setting.GetGlobalSettings().PassThroughRequestEnabled = oldPass })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, kitdto.ChannelSettings{
		ModelSystemPrompts: map[string]string{"gpt-4o": "configured prompt"},
	})
	request := kitdto.OpenAIResponsesRequest{
		Model: "gpt-4o", Input: []byte(`"hello"`), Instructions: []byte(`"client prompt"`),
	}
	info := relaycommon.GenRelayInfoResponses(c, &request)
	for range 2 {
		payload, apiErr := buildResponsesWSCreatePayload(c, info, request, []byte(`true`), "stream-1")
		require.Nil(t, apiErr)
		var event map[string]any
		require.NoError(t, common.Unmarshal(payload, &event))
		assert.Equal(t, "response.create", event["type"])
		assert.Equal(t, "configured prompt\nclient prompt", event["instructions"])
		assert.Equal(t, `"client prompt"`, string(request.Instructions))
	}
}

// TestSystemPromptEstimateSurvivesRealHandlers exercises real handlers against a local capture server.
func TestSystemPromptEstimateSurvivesRealHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	require.NoError(t, model_setting.SetSystemPrompt(`{"models":{"gpt-4o":"configured system prompt"}}`))
	oldPass := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = oldPass
		require.NoError(t, model_setting.SetSystemPrompt(`{"models":{}}`))
	})
	for _, tc := range []struct {
		name, path string
		channel    int
		format     projecttypes.RelayFormat
		raw        string
	}{
		{"chat_openai", "/v1/chat/completions", constant.ChannelTypeOpenAI, projecttypes.RelayFormatOpenAI, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`},
		{"chat_claude", "/v1/chat/completions", constant.ChannelTypeAnthropic, projecttypes.RelayFormatOpenAI, `{"model":"gpt-4o","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`},
		{"chat_gemini", "/v1/chat/completions", constant.ChannelTypeGemini, projecttypes.RelayFormatOpenAI, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`},
		{"claude_native", "/v1/messages", constant.ChannelTypeAnthropic, projecttypes.RelayFormatClaude, `{"model":"gpt-4o","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`},
		{"gemini_native", "/v1beta/models/gpt-4o:generateContent", constant.ChannelTypeGemini, projecttypes.RelayFormatGemini, `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				captured <- string(data)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":{"message":"audit stops before settlement","type":"invalid_request_error"}}`)
			}))
			defer server.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(tc.raw))
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
			common.SetContextKey(c, constant.ContextKeyChannelType, tc.channel)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "fixture-key")
			var req kitdto.Request
			switch tc.format {
			case projecttypes.RelayFormatClaude:
				req = &kitdto.ClaudeRequest{}
			case projecttypes.RelayFormatGemini:
				req = &kitdto.GeminiChatRequest{}
			default:
				req = &kitdto.GeneralOpenAIRequest{}
			}
			require.NoError(t, common.Unmarshal([]byte(tc.raw), req))
			req.SetModelName("gpt-4o")
			info, err := relaycommon.GenRelayInfo(c, tc.format, req, nil)
			require.NoError(t, err)
			preview, previewErr := PreviewSystemPromptTokens(c, info, req, rootdto.ChannelSettings{}, "gpt-4o", false)
			require.Nil(t, previewErr)
			require.Greater(t, preview.PromptTokens, 0)
			info.SetEstimatePromptTokens(preview.BaseInputTokens)
			info.SetInjectedPromptTokenDelta(preview.PromptTokens)
			var apiErr *projecttypes.NewAPIError
			switch tc.format {
			case projecttypes.RelayFormatClaude:
				apiErr = ClaudeHelper(c, info)
			case projecttypes.RelayFormatGemini:
				apiErr = GeminiHelper(c, info)
			default:
				apiErr = TextHelper(c, info)
			}
			require.NotNil(t, apiErr)
			var data string
			select {
			case data = <-captured:
			default:
				t.Fatalf("upstream was not reached: %v", apiErr)
			}
			require.Equal(t, 1, strings.Count(data, "configured system prompt"))
			assert.Equal(t, preview.FinalInputTokens, info.GetEstimatePromptTokens(), "injected token estimate must survive the handler")
			assert.True(t, c.GetBool(string(constant.ContextKeySystemPromptApplied)), "applied prompt audit marker is required")
		})
	}
}
