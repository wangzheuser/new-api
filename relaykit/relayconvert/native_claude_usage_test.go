package relayconvert

import (
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestNativeClaudeSparseUsagePreservesCache protects start-frame input and cache across output-only deltas.
func TestNativeClaudeSparseUsagePreservesCache(t *testing.T) {
	state, err := NewResponseStreamState(types.RelayFormatClaude, types.RelayFormatClaude, ResponseStreamOptions{})
	require.NoError(t, err)
	start := &dto.ClaudeResponse{Type: "message_start", Message: &dto.ClaudeMediaMessage{Usage: &dto.ClaudeUsage{InputTokens: 100, CacheReadInputTokens: 40, CacheCreationInputTokens: 20}}}
	_, err = ConvertStreamResponseChunk(nil, nil, state, start)
	require.NoError(t, err)
	delta := &dto.ClaudeResponse{Type: "message_delta", Usage: &dto.ClaudeUsage{OutputTokens: 10}}
	_, err = ConvertStreamResponseChunk(nil, nil, state, delta)
	require.NoError(t, err)
	usage := state.Usage()
	require.NotNil(t, usage)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Equal(t, 10, usage.CompletionTokens)
	assert.Equal(t, 40, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 20, usage.PromptTokensDetails.CachedCreationTokens)
	require.NotNil(t, usage.BillingUsage)
	assert.Equal(t, 40, usage.BillingUsage.ClaudeUsage.CacheReadInputTokens)
	assert.Zero(t, delta.Usage.InputTokens)
	assert.Zero(t, delta.Usage.CacheReadInputTokens)
}
