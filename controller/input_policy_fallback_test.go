package controller

import (
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

// TestInputPolicyFallbackAllowsTruncationOnlyOnEligibleRequests preserves early rejection for unsupported shapes.
func TestInputPolicyFallbackAllowsTruncationOnlyOnEligibleRequests(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			db := setupContextFallbackTestDB(t)
			settings := dto.ChannelSettings{
				ModelSystemPrompts:    map[string]string{"MODEL_A": strings.Repeat("source prompt ", 80)},
				ModelContextFallbacks: map[string]dto.ModelContextFallback{"MODEL_A": {SourceContextWindowTokens: 64, FallbackModel: "MODEL_B", FallbackContextWindowTokens: 16, RouteMode: dto.ContextFallbackModeSame}},
			}
			source := newContextFallbackChannel("source", "MODEL_A,MODEL_B", settings)
			if enabled {
				source.SetOtherSettings(dto.ChannelOtherSettings{ContextTruncation: &dto.ContextTruncationPolicy{Models: map[string]dto.ContextTruncationRule{"MODEL_B": {Mode: "custom", WindowTokens: 16}}}})
			}
			require.NoError(t, db.Create(source).Error)
			c := setupContextFallbackGinContext(t, source, "MODEL_A")
			info := &relaycommon.RelayInfo{RequestedModelName: "MODEL_A", RoutingModelName: "MODEL_A", AttemptModelName: "MODEL_A", OriginModelName: "MODEL_A", RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI}
			_, err := prepareContextFallback(c, info, newContextFallbackRequest(), 0)
			if enabled {
				require.Nil(t, err)
				assert.Equal(t, "MODEL_B", info.GetAttemptModelName())
				assert.Equal(t, "MODEL_A", info.GetBillingModelName())
			} else {
				require.NotNil(t, err)
				assert.Equal(t, types.ErrorCode("context_length_exceeded"), err.GetErrorCode())
			}
		})
	}
}
