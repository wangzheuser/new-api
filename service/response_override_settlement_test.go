package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvaluateResponseOverrideBeforeSettlementClassifiesUsageProvenance keeps
// client-injected usage from being mistaken for provider-reported usage.
func TestEvaluateResponseOverrideBeforeSettlementClassifiesUsageProvenance(t *testing.T) {
	tests := []struct {
		name          string
		providerBody  string
		usage         *dto.Usage
		localEstimate bool
		expected      string
	}{
		{
			name:         "upstream",
			providerBody: `{"choices":[{"finish_reason":"content_filter","message":{"content":""}}],"usage":{"prompt_tokens":9,"completion_tokens":0,"total_tokens":9}}`,
			usage:        &dto.Usage{PromptTokens: 9, TotalTokens: 9},
			expected:     relaycommon.ResponseUsageUpstream,
		},
		{
			name:          "estimated",
			providerBody:  `{"choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`,
			usage:         &dto.Usage{PromptTokens: 9, TotalTokens: 9},
			localEstimate: true,
			expected:      relaycommon.ResponseUsageEstimated,
		},
		{
			name:         "absent",
			providerBody: `{"choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`,
			expected:     relaycommon.ResponseUsageAbsent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := newSettlementResponseOverrideInfo()
			relaycommon.StartResponseOverrideBuffer(c, info)
			info.MergeResponseSemantics(types.RelayFormatOpenAI, []byte(test.providerBody))
			if test.localEstimate {
				common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
			}
			clientBody := []byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}],"usage":{"prompt_tokens":9,"completion_tokens":0,"total_tokens":9}}`)
			_, err := c.Writer.Write(clientBody)
			require.NoError(t, err)

			decision := EvaluateResponseOverrideBeforeSettlement(c, info, test.usage, http.StatusOK)

			require.NotNil(t, decision)
			assert.True(t, decision.Applied)
			assert.Equal(t, test.expected, decision.Semantics.Response.UsageState)
			assert.Equal(t, test.expected, info.ResponseSemantics.Response.UsageState)
		})
	}
}

// TestEvaluateResponseOverrideBeforeSettlementPrecedesBillingSettle protects
// the lifecycle boundary without coupling the test to database-backed logging.
func TestEvaluateResponseOverrideBeforeSettlementPrecedesBillingSettle(t *testing.T) {
	tests := []struct {
		name        string
		usage       *dto.Usage
		actualQuota int
	}{
		{
			name:        "zero completion tokens remain billable",
			usage:       &dto.Usage{PromptTokens: 9, CompletionTokens: 0, TotalTokens: 9},
			actualQuota: 9,
		},
		{
			name:        "positive completion tokens remain billable",
			usage:       &dto.Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12},
			actualQuota: 12,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := newSettlementResponseOverrideInfo()
			info.UserQuota = 1 << 30
			relaycommon.StartResponseOverrideBuffer(c, info)
			body := []byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}],"usage":{"total_tokens":9}}`)
			info.MergeResponseSemantics(types.RelayFormatOpenAI, body)
			_, err := c.Writer.Write(body)
			require.NoError(t, err)
			settler := &responseOverrideBillingSpy{beforeSettle: func() {
				require.NotNil(t, info.ResponseOverride)
				assert.True(t, info.ResponseOverride.Evaluated)
				assert.True(t, info.ResponseOverride.Applied)
			}}
			info.Billing = settler

			decision := EvaluateResponseOverrideBeforeSettlement(c, info, test.usage, http.StatusOK)
			require.NotNil(t, decision)
			require.NoError(t, SettleBilling(c, info, test.actualQuota))

			assert.Equal(t, 1, settler.settleCalls)
			assert.Equal(t, 0, settler.refundCalls)
			assert.Equal(t, test.actualQuota, settler.settledQuota)
		})
	}
}

type responseOverrideBillingSpy struct {
	settleCalls  int
	refundCalls  int
	settledQuota int
	beforeSettle func()
}

// Settle records the charged quota and verifies the response decision exists first.
func (spy *responseOverrideBillingSpy) Settle(actualQuota int) error {
	spy.settleCalls++
	spy.settledQuota = actualQuota
	if spy.beforeSettle != nil {
		spy.beforeSettle()
	}
	return nil
}

// Refund records an unexpected refund request.
func (spy *responseOverrideBillingSpy) Refund(*gin.Context) { spy.refundCalls++ }

// NeedsRefund reports that the test session has no pending refund.
func (spy *responseOverrideBillingSpy) NeedsRefund() bool { return false }

// GetPreConsumedQuota returns the test session's zero pre-consumption amount.
func (spy *responseOverrideBillingSpy) GetPreConsumedQuota() int { return 0 }

// Reserve accepts quota reservation because this spy only observes settlement.
func (spy *responseOverrideBillingSpy) Reserve(int) error { return nil }

// newSettlementResponseOverrideInfo builds the shared semantic rejection fixture.
func newSettlementResponseOverrideInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ParamOverride: map[string]interface{}{
			"operations": []interface{}{
				map[string]interface{}{
					"phase": "response",
					"mode":  "return_error",
					"value": map[string]interface{}{"message": "blocked"},
					"conditions": []interface{}{
						map[string]interface{}{
							"source": "semantic",
							"path":   "response.rejection_state",
							"mode":   "full",
							"value":  relaycommon.ResponseRejectionAll,
						},
					},
				},
			},
		}},
	}
}
