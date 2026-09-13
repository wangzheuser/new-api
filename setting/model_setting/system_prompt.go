package model_setting

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const SystemPromptOption = "system_prompt.policy"

var systemPromptPolicy atomic.Pointer[dto.ModelSystemPromptPolicy]

// ParseSystemPrompt validates a complete global model prompt policy.
func ParseSystemPrompt(value string) (*dto.ModelSystemPromptPolicy, error) {
	if len(value) > dto.MaxChannelSettingBytes {
		return nil, fmt.Errorf("system_prompt: policy exceeds 64 KiB")
	}
	var p *dto.ModelSystemPromptPolicy
	if err := common.UnmarshalJsonStr(value, &p); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("system_prompt: policy must be an object")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// SetSystemPrompt publishes one immutable global model prompt policy.
func SetSystemPrompt(value string) error {
	p, err := ParseSystemPrompt(value)
	if err != nil {
		systemPromptPolicy.Store(&dto.ModelSystemPromptPolicy{Models: map[string]string{}})
		return err
	}
	systemPromptPolicy.Store(p)
	return nil
}

// ResolveSystemPrompt applies channel-specific model prompts before global defaults.
func ResolveSystemPrompt(channel dto.ChannelSettings, requestedModel, attemptModel string, fallbackActive bool) (string, bool, string, string) {
	if prompt, ok := channel.ModelSystemPrompts[requestedModel]; ok && strings.TrimSpace(prompt) != "" {
		return prompt, true, "model_requested", requestedModel
	}
	if fallbackActive && attemptModel != "" && attemptModel != requestedModel {
		if prompt, ok := channel.ModelSystemPrompts[attemptModel]; ok && strings.TrimSpace(prompt) != "" {
			return prompt, true, "model_attempt", attemptModel
		}
	}
	if global := systemPromptPolicy.Load(); global != nil {
		if prompt, ok := global.Models[requestedModel]; ok && strings.TrimSpace(prompt) != "" {
			return prompt, true, "global_model_requested", requestedModel
		}
		if fallbackActive && attemptModel != "" && attemptModel != requestedModel {
			if prompt, ok := global.Models[attemptModel]; ok && strings.TrimSpace(prompt) != "" {
				return prompt, true, "global_model_attempt", attemptModel
			}
		}
	}
	return channel.SystemPrompt, channel.SystemPromptOverride, "channel_default", ""
}

// HasSystemPrompt reports whether any effective prompt will be injected.
func HasSystemPrompt(channel dto.ChannelSettings, requestedModel, attemptModel string, fallbackActive bool) bool {
	prompt, _, _, _ := ResolveSystemPrompt(channel, requestedModel, attemptModel, fallbackActive)
	return strings.TrimSpace(prompt) != ""
}
