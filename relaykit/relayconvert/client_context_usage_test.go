package relayconvert

import (
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestConvertedClaudeSparseUsageKeepsBilling protects the real upstream snapshot through every converted client protocol.
func TestConvertedClaudeSparseUsageKeepsBilling(t *testing.T) {
	for _, target := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatGemini} {
		t.Run(string(target), func(t *testing.T) {
			state, err := NewResponseStreamState(types.RelayFormatClaude, target, ResponseStreamOptions{})
			require.NoError(t, err)
			info := &convmeta.Values{}
			start := &dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{Usage: &dto.ClaudeUsage{InputTokens: 100, CacheReadInputTokens: 40, CacheCreationInputTokens: 20}}}
			_, err = ConvertStreamResponseChunk(nil, info, state, start)
			require.NoError(t, err)
			delta := &dto.ClaudeResponse{Type: "message_delta", Usage: &dto.ClaudeUsage{OutputTokens: 10}}
			_, err = ConvertStreamResponseChunk(nil, info, state, delta)
			require.NoError(t, err)
			usage := state.Usage()
			require.NotNil(t, usage)
			require.NotNil(t, usage.BillingUsage)
			raw := usage.BillingUsage.ClaudeUsage
			require.NotNil(t, raw)
			assert.Equal(t, 100, raw.InputTokens)
			assert.Equal(t, 40, raw.CacheReadInputTokens)
			assert.Equal(t, 20, raw.CacheCreationInputTokens)
			assert.Equal(t, 10, raw.OutputTokens)
			assert.Zero(t, delta.Usage.InputTokens, "raw input event is immutable")
			assert.Zero(t, delta.Usage.CacheReadInputTokens)
		})
	}
}
