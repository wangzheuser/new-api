package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rejectedPolicyReserve models insufficient balance at the real pre-send reserve hook.
type rejectedPolicyReserve struct{ reserveCapture }

// Reserve rejects funding without producing any upstream side effect.
func (r *rejectedPolicyReserve) Reserve(int) error { return errors.New("insufficient balance") }

// TestInputPolicyPreSendGates covers counting-off, post-conversion budget, overrides and insufficient quota.
func TestInputPolicyPreSendGates(t *testing.T) {
	service.InitHttpClient()
	oldCount := constant.CountToken
	constant.CountToken = false
	t.Cleanup(func() { constant.CountToken = oldCount })
	for _, scenario := range []string{"trim", "missing-output", "protected", "override", "balance", "final-growth", "pass-through", "unsupported-media"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
			defer upstream.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := convertedResponsesViaChatTestInfo(upstream.URL, false)
			info.RelayFormat = types.RelayFormatOpenAI
			info.RequestConversionChain = []types.RelayFormat{types.RelayFormatOpenAI}
			info.ChannelRoutePlan = nil
			info.ChannelOtherSettings.ContextTruncation = &dto.ContextTruncationPolicy{Models: map[string]dto.ContextTruncationRule{"MODEL_X": {Mode: "custom", WindowTokens: 256}}}
			request := &dto.GeneralOpenAIRequest{Model: "MODEL_X", MaxTokens: common.GetPointer(uint(64)), Messages: []dto.Message{
				{Role: "user", Content: strings.Repeat("disposable history ", 1000)}, {Role: "assistant", Content: "old"}, {Role: "user", Content: "latest"},
			}}
			if scenario == "missing-output" {
				request.MaxTokens = nil
			}
			if scenario == "protected" {
				request.Messages = request.Messages[:1]
			}
			if scenario == "override" {
				info.ParamOverride = map[string]interface{}{"messages.0.content": "replacement"}
			}
			if scenario == "balance" {
				info.Billing = &rejectedPolicyReserve{}
			}
			if scenario == "unsupported-media" {
				request.Messages[2].Content = []map[string]string{{"type": "input_audio", "data": "audio"}}
			}
			err := prepareTextInputPolicies(c, info, request, scenario == "pass-through")
			switch scenario {
			case "missing-output", "protected", "override", "balance", "unsupported-media":
				require.NotNil(t, err)
				assert.Equal(t, 0, calls)
				assert.Len(t, request.Messages, map[bool]int{true: 1, false: 3}[scenario == "protected"])
			case "pass-through":
				require.Nil(t, err)
				assert.False(t, info.ContextTruncation.Applied)
				assert.Nil(t, info.ValidateInputPolicyBody)
				assert.Len(t, request.Messages, 3)
			default:
				require.Nil(t, err)
				assert.True(t, info.ContextTruncation.Applied)
				assert.Len(t, request.Messages, 1)
				assert.False(t, constant.CountToken)
				require.NotNil(t, info.ValidateInputPolicyBody)
				if scenario == "final-growth" {
					request.Messages[0].Content = strings.Repeat("added after preparation ", 1000)
					body, e := common.Marshal(request)
					require.NoError(t, e)
					adaptor := GetAdaptor(constant.APITypeOpenAI)
					adaptor.Init(info)
					_, e = adaptor.DoRequest(c, info, strings.NewReader(string(body)))
					require.ErrorContains(t, e, "final_budget_exceeded")
					assert.Zero(t, calls)
				}
			}
		})
	}
}

// TestInputPolicyAttemptIsolation rebuilds each candidate from the original and preserves random draws.
func TestInputPolicyAttemptIsolation(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	info := convertedResponsesViaChatTestInfo("http://127.0.0.1", false)
	info.ChannelOtherSettings.CacheUsageSimulation = &dto.CacheUsageSimulationPolicy{Mode: "custom"}
	original := &dto.GeneralOpenAIRequest{Model: "MODEL_X", MaxTokens: common.GetPointer(uint(64)), Messages: []dto.Message{{Role: "user", Content: strings.Repeat("history ", 1000)}, {Role: "user", Content: "latest"}}}
	for _, window := range []int{256, 100000} {
		info.ChannelOtherSettings.ContextTruncation = &dto.ContextTruncationPolicy{Models: map[string]dto.ContextTruncationRule{"MODEL_X": {Mode: "custom", WindowTokens: window}}}
		sample := info.CacheUsageSamples
		info.InitInputPolicyState()
		if sample != nil {
			assert.Same(t, sample, info.CacheUsageSamples)
		}
		assert.Nil(t, info.ValidateInputPolicyBody)
		copy, e := copySystemPromptRequest(original)
		require.NoError(t, e)
		require.Nil(t, prepareTextInputPolicies(c, info, copy, false))
		assert.Equal(t, window == 256, info.ContextTruncation.Applied)
		assert.Len(t, original.Messages, 2)
		assert.Empty(t, info.CacheUsageSimulation.Reason)
	}
}
