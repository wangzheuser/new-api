package relay

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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

	openAIRequest := &dto.GeneralOpenAIRequest{
		Model: "model-a",
		Messages: []dto.Message{{
			Role:    "system",
			Content: "client prompt",
		}},
	}
	applyOpenAISystemPrompt(c, openAIRequest, "model prompt", true)
	assert.Equal(t, "model prompt\nclient prompt", openAIRequest.Messages[0].StringContent())

	responsesInstructions, err := common.Marshal("client prompt")
	require.NoError(t, err)
	responsesRequest := &dto.OpenAIResponsesRequest{Instructions: responsesInstructions}
	applied, err := applyResponsesSystemPrompt(c, responsesRequest, "model prompt", true)
	require.NoError(t, err)
	require.True(t, applied)
	var mergedInstructions string
	require.NoError(t, common.Unmarshal(responsesRequest.Instructions, &mergedInstructions))
	assert.Equal(t, "model prompt\nclient prompt", mergedInstructions)

	claudeRequest := &dto.ClaudeRequest{System: "client prompt"}
	applyClaudeSystemPrompt(c, claudeRequest, "model prompt", true)
	assert.Equal(t, "model prompt\nclient prompt", claudeRequest.GetStringSystem())

	geminiRequest := &dto.GeminiChatRequest{
		SystemInstructions: &dto.GeminiChatContent{
			Parts: []dto.GeminiPart{{Text: "client prompt"}},
		},
	}
	applyGeminiSystemPrompt(c, geminiRequest, "model prompt", true)
	assert.Equal(t, "model prompt\nclient prompt", geminiRequest.SystemInstructions.Parts[0].Text)
}

func TestResolveSystemPromptUsesImmutableRequestedModel(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RequestedModelName: "public-model",
		OriginModelName:    "mapped-model",
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{
			ModelSystemPrompts: map[string]string{"public-model": "public prompt"},
		}},
	}

	prompt, prepend := resolveSystemPrompt(info)

	assert.Equal(t, "public prompt", prompt)
	assert.True(t, prepend)
}

func TestInjectedSystemPromptUpdatesEstimateAndReserve(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-3.5-turbo")

	request := &dto.GeneralOpenAIRequest{
		Model:    "gpt-3.5-turbo",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
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
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{
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
	request := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "system", Content: "client prompt"}},
	}

	applyOpenAISystemPrompt(nil, request, "channel default", false)

	assert.Equal(t, "client prompt", request.Messages[0].StringContent())
}
