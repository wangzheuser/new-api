package dto

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

type ModelSystemPromptPolicy struct {
	Models map[string]string `json:"models"`
}

func (p ModelSystemPromptPolicy) Validate() error {
	if len(p.Models) > MaxModelSystemPromptEntries {
		return fmt.Errorf("model system prompt entries cannot exceed %d", MaxModelSystemPromptEntries)
	}
	for model, prompt := range p.Models {
		if strings.TrimSpace(model) == "" || strings.TrimSpace(model) != model || len(model) > 255 {
			return fmt.Errorf("invalid model system prompt model: %s", model)
		}
		if strings.TrimSpace(prompt) == "" || len(prompt) > MaxModelSystemPromptBytes {
			return fmt.Errorf("invalid model system prompt for model: %s", model)
		}
	}
	return nil
}

func (p ChannelProtocolPolicy) EffectiveMaxQuality() ProtocolConversionQuality {
	if p.MaxQuality == "" {
		return ProtocolConversionQualityFair
	}
	return p.MaxQuality
}

func (p ChannelProtocolPolicy) NativeForModel(model string) (map[types.EndpointType]ProtocolCapability, string) {
	if override, ok := p.ModelOverrides[model]; ok {
		return override.Native, "model_override"
	}
	return p.Native, "channel_default"
}

func (p ChannelProtocolPolicy) HasNormalizedCapability() bool {
	for _, capability := range p.Native {
		if capability.EffectiveMode() == ProtocolHandlingModeNormalized {
			return true
		}
	}
	for _, profile := range p.ModelOverrides {
		for _, capability := range profile.Native {
			if capability.EffectiveMode() == ProtocolHandlingModeNormalized {
				return true
			}
		}
	}
	return false
}

func (c ProtocolCapability) EffectiveMode() ProtocolHandlingMode {
	if c.Mode == "" {
		return ProtocolHandlingModeNative
	}
	return c.Mode
}

func (c ProtocolCapability) EffectiveReasoningHistoryPolicy() types.ReasoningHistoryPolicy {
	if c.ReasoningHistory == "" {
		return types.ReasoningHistoryPolicyPreserve
	}
	return c.ReasoningHistory
}

func (p ChannelProtocolPolicy) Validate() error {
	if err := validateNativeProtocolCapabilities(p.Native, "channel"); err != nil {
		return err
	}
	if len(p.ModelOverrides) > MaxModelProtocolOverrides {
		return fmt.Errorf("model protocol overrides cannot exceed %d", MaxModelProtocolOverrides)
	}
	for model, profile := range p.ModelOverrides {
		if strings.TrimSpace(model) == "" || strings.TrimSpace(model) != model || len(model) > 255 {
			return fmt.Errorf("invalid model protocol override: %s", model)
		}
		if err := validateNativeProtocolCapabilities(profile.Native, "model "+model); err != nil {
			return err
		}
	}
	quality := p.EffectiveMaxQuality()
	if quality != ProtocolConversionQualityGood && quality != ProtocolConversionQualityFair {
		return fmt.Errorf("invalid protocol conversion quality: %s", p.MaxQuality)
	}
	return nil
}

func validateNativeProtocolCapabilities(capabilities map[types.EndpointType]ProtocolCapability, scope string) error {
	if len(capabilities) == 0 {
		return fmt.Errorf("%s native protocol capabilities are required", scope)
	}
	for endpointType, capability := range capabilities {
		if !IsTextProtocolEndpointType(endpointType) {
			return fmt.Errorf("invalid text protocol endpoint type: %s", endpointType)
		}
		if !capability.NonStream && !capability.Stream {
			return fmt.Errorf("protocol capability %s for %s must enable non-stream or stream", endpointType, scope)
		}
		mode := capability.EffectiveMode()
		if mode != ProtocolHandlingModeNative && mode != ProtocolHandlingModeNormalized {
			return fmt.Errorf("invalid protocol handling mode %s for %s in %s", capability.Mode, endpointType, scope)
		}
		if mode == ProtocolHandlingModeNormalized && endpointType != types.EndpointTypeAnthropic && endpointType != types.EndpointTypeOpenAIResponse {
			return fmt.Errorf("normalized protocol handling is unsupported for %s in %s", endpointType, scope)
		}
		if capability.ReasoningHistory != "" {
			if mode != ProtocolHandlingModeNormalized || endpointType != types.EndpointTypeAnthropic {
				return fmt.Errorf("reasoning history policy is only supported for normalized anthropic capabilities in %s", scope)
			}
			policy := capability.EffectiveReasoningHistoryPolicy()
			if policy != types.ReasoningHistoryPolicyPreserve && policy != types.ReasoningHistoryPolicyStrip {
				return fmt.Errorf("invalid reasoning history policy %s for %s in %s", capability.ReasoningHistory, endpointType, scope)
			}
		}
	}
	return nil
}

func IsTextProtocolEndpointType(endpointType types.EndpointType) bool {
	switch endpointType {
	case types.EndpointTypeOpenAI, types.EndpointTypeOpenAIResponse, types.EndpointTypeAnthropic, types.EndpointTypeGemini:
		return true
	default:
		return false
	}
}

func (s ChannelSettings) ResolveSystemPrompt(model string) (string, bool) {
	if prompt, ok := s.ModelSystemPrompts[model]; ok && strings.TrimSpace(prompt) != "" {
		return prompt, true
	}
	return s.SystemPrompt, s.SystemPromptOverride
}

func (s ChannelSettings) ResolveSystemPromptForAttempt(requestedModel, attemptModel string, fallbackActive bool) (string, bool, string, string) {
	if prompt, ok := s.ModelSystemPrompts[requestedModel]; ok && strings.TrimSpace(prompt) != "" {
		return prompt, true, "model_requested", requestedModel
	}
	if fallbackActive && attemptModel != "" && attemptModel != requestedModel {
		if prompt, ok := s.ModelSystemPrompts[attemptModel]; ok && strings.TrimSpace(prompt) != "" {
			return prompt, true, "model_attempt", attemptModel
		}
	}
	return s.SystemPrompt, s.SystemPromptOverride, "channel_default", ""
}

func (s ChannelSettings) ResolveContextFallback(requestedModel string) (ModelContextFallback, bool) {
	rule, ok := s.ModelContextFallbacks[requestedModel]
	return rule, ok
}

func (s ChannelSettings) ValidateSystemPrompts() error {
	if len(s.ModelSystemPrompts) > MaxModelSystemPromptEntries {
		return fmt.Errorf("model system prompt entries cannot exceed %d", MaxModelSystemPromptEntries)
	}
	for model, prompt := range s.ModelSystemPrompts {
		if strings.TrimSpace(model) == "" || strings.TrimSpace(model) != model || len(model) > 255 {
			return fmt.Errorf("invalid model system prompt model: %s", model)
		}
		if strings.TrimSpace(prompt) == "" || len(prompt) > MaxModelSystemPromptBytes {
			return fmt.Errorf("invalid model system prompt for model: %s", model)
		}
	}
	return nil
}

func (s ChannelSettings) ValidateContextFallbacks() error {
	if len(s.ModelContextFallbacks) > MaxModelContextFallbacks {
		return fmt.Errorf("model context fallback entries cannot exceed %d", MaxModelContextFallbacks)
	}
	for sourceModel, rule := range s.ModelContextFallbacks {
		if strings.TrimSpace(sourceModel) == "" || strings.TrimSpace(sourceModel) != sourceModel || len(sourceModel) > 255 {
			return fmt.Errorf("invalid model context fallback source model: %s", sourceModel)
		}
		if rule.SourceContextWindowTokens <= 0 {
			return fmt.Errorf("source context window must be positive: %s", sourceModel)
		}
		threshold := rule.EffectiveThresholdPercent()
		if threshold < 1 || threshold > 100 {
			return fmt.Errorf("context fallback threshold must be between 1 and 100: %s", sourceModel)
		}
		fallbackModel := strings.TrimSpace(rule.FallbackModel)
		if fallbackModel == "" || fallbackModel != rule.FallbackModel || len(fallbackModel) > 255 || fallbackModel == sourceModel {
			return fmt.Errorf("invalid context fallback model: %s", sourceModel)
		}
		if rule.FallbackContextWindowTokens <= 0 {
			return fmt.Errorf("fallback context window must be positive: %s", sourceModel)
		}
		if rule.RouteMode != ContextFallbackModeSame && rule.RouteMode != ContextFallbackModeCross {
			return fmt.Errorf("invalid context fallback route mode: %s", sourceModel)
		}
		seen := make(map[int]struct{}, len(rule.TargetChannelIDs))
		for _, channelID := range rule.TargetChannelIDs {
			if channelID <= 0 {
				return fmt.Errorf("invalid context fallback target channel id: %s", sourceModel)
			}
			if _, ok := seen[channelID]; ok {
				return fmt.Errorf("duplicate context fallback target channel id: %s", sourceModel)
			}
			seen[channelID] = struct{}{}
		}
		if rule.RouteMode == ContextFallbackModeSame && len(rule.TargetChannelIDs) > 0 {
			return fmt.Errorf("same-channel context fallback cannot specify target channels: %s", sourceModel)
		}
	}
	return nil
}
