package dto

import (
	"fmt"
	"strings"
)

// ContextTruncationRule configures one logical model, before upstream mapping.
type ContextTruncationRule struct {
	Mode                string `json:"mode"`
	WindowTokens        int    `json:"window_tokens,omitempty"`
	ThresholdPercent    *int   `json:"threshold_percent,omitempty"`
	SafetyTokens        *int   `json:"safety_tokens"`
	KeepRecentTurns     *int   `json:"keep_recent_turns,omitempty"`
	OutputReserveTokens *int   `json:"output_reserve_tokens"`
}

// ContextTruncationPolicy is also used for the channel's model overrides.
type ContextTruncationPolicy struct {
	ForceDisabled bool                             `json:"force_disabled,omitempty"`
	Models        map[string]ContextTruncationRule `json:"models"`
}

// Threshold returns the configured threshold, preserving an explicit invalid zero for validation.
func (r ContextTruncationRule) Threshold() int {
	if r.ThresholdPercent != nil {
		return *r.ThresholdPercent
	}
	return 90
}

// Keep returns the minimum number of protected business turns.
func (r ContextTruncationRule) Keep() int {
	if r.KeepRecentTurns != nil {
		return *r.KeepRecentTurns
	}
	return 1
}

// Safety returns a bounded rounding-up margin, or the explicit override.
func (r ContextTruncationRule) Safety() int {
	if r.SafetyTokens != nil {
		return *r.SafetyTokens
	}
	return min(int((int64(r.WindowTokens)*2+99)/100), 8192)
}

// Budget subtracts output capacity without silently changing the generation parameters.
func (r ContextTruncationRule) Budget(output int) (int, error) {
	if r.OutputReserveTokens != nil {
		output = max(output, *r.OutputReserveTokens)
	}
	if output <= 0 {
		return 0, fmt.Errorf("context_truncation_output_reserve_required")
	}
	n := min(int64(r.WindowTokens)*int64(r.Threshold())/100, int64(r.WindowTokens)-int64(output)-int64(r.Safety()))
	if n <= 0 {
		return 0, fmt.Errorf("context_length_exceeded")
	}
	return int(n), nil
}

// Validate checks the whole rule before it is published.
func (r ContextTruncationRule) Validate(channel bool) error {
	if r.Mode == "off" || (channel && (r.Mode == "inherit" || r.Mode == "")) {
		return nil
	}
	if r.Mode != "custom" {
		return fmt.Errorf("context_truncation: invalid mode")
	}
	if r.WindowTokens <= 0 || int64(r.WindowTokens) > 2147483647 || r.Threshold() < 1 || r.Threshold() > 100 || r.Keep() < 1 || r.Keep() > 256 {
		return fmt.Errorf("context_truncation: invalid window, threshold or retained turns")
	}
	if r.Safety() < 0 || r.Safety() >= r.WindowTokens {
		return fmt.Errorf("context_truncation: invalid safety margin")
	}
	if r.OutputReserveTokens != nil {
		if *r.OutputReserveTokens <= 0 || *r.OutputReserveTokens >= r.WindowTokens {
			return fmt.Errorf("context_truncation: invalid output reserve")
		}
		if _, err := r.Budget(0); err != nil {
			return err
		}
	}
	return nil
}

// Validate validates model keys and all rules, without partial updates.
func (p ContextTruncationPolicy) Validate(channel bool) error {
	if len(p.Models) > 256 || (channel && p.ForceDisabled) {
		return fmt.Errorf("context_truncation: invalid policy scope or model count")
	}
	for id, r := range p.Models {
		if id == "" || len(id) > 255 || strings.TrimSpace(id) != id {
			return fmt.Errorf("context_truncation: invalid model ID")
		}
		if err := r.Validate(channel); err != nil {
			return err
		}
	}
	return nil
}
