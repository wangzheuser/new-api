package common

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRelayInfoGetFinalRequestRelayFormatPrefersExplicitFinal(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
		FinalRequestRelayFormat: types.RelayFormatOpenAIResponses,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToConversionChain(t *testing.T) {
	info := &RelayInfo{
		RelayFormat:            types.RelayFormatOpenAI,
		RequestConversionChain: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude},
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatFallsBackToRelayFormat(t *testing.T) {
	info := &RelayInfo{
		RelayFormat: types.RelayFormatGemini,
	}

	require.Equal(t, types.RelayFormat(types.RelayFormatGemini), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoGetFinalRequestRelayFormatNilReceiver(t *testing.T) {
	var info *RelayInfo
	require.Equal(t, types.RelayFormat(""), info.GetFinalRequestRelayFormat())
}

func TestRelayInfoAddReasoningHistoryAuditAggregatesCountsAndRouteReasons(t *testing.T) {
	info := &RelayInfo{}
	info.AddReasoningHistoryAudit(
		types.RelayFormatClaude,
		types.RelayFormatOpenAI,
		ReasoningHistoryReasonPreserved,
		1, 0, 0, 0,
	)
	info.AddDroppedReasoningOnlyMessages(types.RelayFormatClaude, types.RelayFormatOpenAI, 3)
	info.AddReasoningHistoryAudit(
		types.RelayFormatClaude,
		types.RelayFormatOpenAI,
		ReasoningHistoryReasonOpaqueBlockSkipped,
		0, 0, 2, 0,
	)
	info.AddReasoningHistoryAudit(
		types.RelayFormatOpenAI,
		types.RelayFormatGemini,
		ReasoningHistoryReasonPreserved,
		1, 0, 0, 0,
	)

	require.True(t, info.HasReasoningHistoryAudit())
	require.NotNil(t, info.ReasoningHistory)
	assert.Equal(t, 2, info.ReasoningHistory.PreservedMessages)
	assert.Equal(t, 3, info.ReasoningHistory.DroppedReasoningOnlyMessages)
	assert.Equal(t, 2, info.ReasoningHistory.OpaqueBlocksSkipped)
	require.Len(t, info.ReasoningHistory.Routes, 2)
	assert.Equal(t, []string{
		ReasoningHistoryReasonPreserved,
		ReasoningHistoryReasonReasoningOnlyDropped,
		ReasoningHistoryReasonOpaqueBlockSkipped,
	}, info.ReasoningHistory.Routes[0].ReasonCodes)
}

func TestRelayInfoAcceptStreamPolicyVersionOnlyForNativeChatAndClaude(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		info     *RelayInfo
		expected string
	}{
		{
			name:     "native chat",
			info:     &RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAI},
			expected: "progressive-v1",
		},
		{
			name:     "native claude",
			info:     &RelayInfo{IsStream: true, RelayFormat: types.RelayFormatClaude},
			expected: "progressive-v1",
		},
		{
			name: "responses excluded",
			info: &RelayInfo{IsStream: true, RelayFormat: types.RelayFormatOpenAIResponses},
		},
		{
			name: "conversion excluded",
			info: &RelayInfo{
				IsStream:                true,
				RelayFormat:             types.RelayFormatClaude,
				FinalRequestRelayFormat: types.RelayFormatOpenAI,
			},
		},
		{
			name: "non stream excluded",
			info: &RelayInfo{RelayFormat: types.RelayFormatOpenAI},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.info.AcceptStreamPolicyVersion("progressive-v1"))
		})
	}
}

func TestRelayInfoResponsesCompactionKeepsClientAndRoutingModelsSeparate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses/compact", nil)
	routingModel := ratio_setting.WithCompactModelSuffix("public-model")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, routingModel)
	request := &dto.OpenAIResponsesCompactionRequest{Model: "public-model"}

	info := GenRelayInfoResponsesCompaction(c, request)
	info.InitChannelMeta(c)

	require.Equal(t, "public-model", info.GetRequestedModelName())
	require.Equal(t, routingModel, info.GetRoutingModelName())
	require.Equal(t, routingModel, info.OriginModelName)
	require.Equal(t, "public-model", request.Model)
}

// TestRelayInfoInitChannelMetaResetsAttemptState verifies retry attempts do not reuse response or tool billing facts.
func TestRelayInfoInitChannelMetaResetsAttemptState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "openai_finish_reason=content_filter")
	c.Set("claude_web_search_requests", 2)
	c.Set("image_generation_call", true)
	c.Set("image_generation_call_quality", "high")
	c.Set("image_generation_call_size", "1024x1024")
	tool := &BuildInToolInfo{ToolName: dto.BuildInToolWebSearchPreview, CallCount: 3, SearchContextSize: "medium"}
	info := &RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		ResponsesUsageInfo: &ResponsesUsageInfo{BuiltInTools: map[string]*BuildInToolInfo{
			dto.BuildInToolWebSearchPreview: tool,
		}},
	}

	info.InitChannelMeta(c)

	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
	assert.Empty(t, common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason))
	assert.Zero(t, c.GetInt("claude_web_search_requests"))
	assert.False(t, c.GetBool("image_generation_call"))
	assert.Empty(t, c.GetString("image_generation_call_quality"))
	assert.Empty(t, c.GetString("image_generation_call_size"))
	assert.Zero(t, tool.CallCount)
	assert.Equal(t, dto.BuildInToolWebSearchPreview, tool.ToolName)
	assert.Equal(t, "medium", tool.SearchContextSize)
}
