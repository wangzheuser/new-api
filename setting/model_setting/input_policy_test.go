package model_setting

import (
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestInputPolicyOverrideAndEmergencyStop verifies whole-policy publication and retry snapshots.
func TestInputPolicyOverrideAndEmergencyStop(t *testing.T) {
	oldT, oldC := truncationPolicy.Load(), cacheSimulationPolicy.Load()
	t.Cleanup(func() { truncationPolicy.Store(oldT); cacheSimulationPolicy.Store(oldC) })
	require.NoError(t, SetContextTruncation(`{"models":{"m":{"mode":"custom","window_tokens":1000,"output_reserve_tokens":100}}}`))
	channel := &dto.ContextTruncationPolicy{Models: map[string]dto.ContextTruncationRule{"m": {Mode: "off"}}}
	_, source, enabled := ResolveContextTruncation(channel, "m")
	assert.Equal(t, "channel", source)
	assert.False(t, enabled)
	snapshot, _, enabled := ResolveContextTruncation(nil, "m")
	assert.True(t, enabled)
	require.NoError(t, SetContextTruncation(`{"force_disabled":true,"models":{}}`))
	channel.Models["m"] = snapshot
	_, _, enabled = ResolveContextTruncation(channel, "m")
	assert.False(t, enabled)
	assert.Equal(t, 1000, snapshot.WindowTokens)
	require.NoError(t, SetCacheUsageSimulation(`{"enabled":false}`))
	_, source, enabled = ResolveCacheUsageSimulation(&dto.CacheUsageSimulationPolicy{Mode: "custom"})
	assert.True(t, enabled)
	assert.Equal(t, "channel", source)
	require.Error(t, SetCacheUsageSimulation(`{"read_trigger_percent":null}`))
	_, _, enabled = ResolveCacheUsageSimulation(&dto.CacheUsageSimulationPolicy{Mode: "custom"})
	assert.False(t, enabled)
	for _, raw := range []string{`null`, `[]`, `{"read_trigger_percent":101}`, `{"creation_token_percent":80}`} {
		_, err := ParseCacheUsageSimulation(raw)
		assert.Error(t, err)
	}
}
