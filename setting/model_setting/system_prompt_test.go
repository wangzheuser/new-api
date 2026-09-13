package model_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSystemPromptChannelThenGlobal(t *testing.T) {
	old := systemPromptPolicy.Load()
	t.Cleanup(func() { systemPromptPolicy.Store(old) })
	require.NoError(t, SetSystemPrompt(`{"models":{"shared":"global shared","global":"global only","fallback":"global fallback"}}`))
	channel := dto.ChannelSettings{SystemPrompt: "channel default", SystemPromptOverride: false, ModelSystemPrompts: map[string]string{"shared": "channel shared", "channel-only": "channel only"}}

	prompt, prepend, source, model := ResolveSystemPrompt(channel, "shared", "", false)
	assert.Equal(t, "channel shared", prompt)
	assert.True(t, prepend)
	assert.Equal(t, "model_requested", source)
	assert.Equal(t, "shared", model)

	prompt, _, source, _ = ResolveSystemPrompt(channel, "global", "", false)
	assert.Equal(t, "global only", prompt)
	assert.Equal(t, "global_model_requested", source)

	prompt, _, source, _ = ResolveSystemPrompt(channel, "missing", "fallback", true)
	assert.Equal(t, "global fallback", prompt)
	assert.Equal(t, "global_model_attempt", source)
}

func TestParseSystemPromptValidation(t *testing.T) {
	require.NoError(t, ValidateInputPolicyOption(SystemPromptOption, `{"models":{"m":"prompt"}}`))
	assert.Error(t, ValidateInputPolicyOption(SystemPromptOption, `{"models":{"m":"  "}}`))
	assert.Error(t, ValidateInputPolicyOption(SystemPromptOption, `{"models":{" m":"prompt"}}`))
}
