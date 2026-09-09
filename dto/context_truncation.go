package dto

import (
	"fmt"
	"math"
	"strings"
)

// MaxOutputTokens bounds output multipliers before request and quota calculations.
const MaxOutputTokens = math.MaxInt32 / 2

// ContextTruncationRule configures one logical model, before upstream mapping.
type ContextTruncationRule struct {
	Mode                string `json:"mode"`
	WindowTokens        int    `json:"window_tokens,omitempty"`
	OutputReserveTokens *int   `json:"output_reserve_tokens"`
}

// ContextTruncationPolicy is also used for the channel's model overrides.
type ContextTruncationPolicy struct {
	ForceDisabled bool                             `json:"force_disabled,omitempty"`
	Models        map[string]ContextTruncationRule `json:"models"`
}

// Threshold keeps headroom without a per-model tuning parameter.
func (r ContextTruncationRule) Threshold() int {
	return 90
}

// Keep returns the minimum number of protected business turns.
func (r ContextTruncationRule) Keep() int {
	return 1
}

// Safety reserves a bounded rounding-up margin for token estimation.
func (r ContextTruncationRule) Safety() int {
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
	if r.WindowTokens <= 0 || int64(r.WindowTokens) > math.MaxInt32 {
		return fmt.Errorf("context_truncation: invalid window")
	}
	if r.Safety() < 0 || r.Safety() >= r.WindowTokens {
		return fmt.Errorf("context_truncation: invalid safety margin")
	}
	if r.OutputReserveTokens == nil {
		return fmt.Errorf("context_truncation: output reserve is required")
	}
	if *r.OutputReserveTokens <= 0 || *r.OutputReserveTokens > MaxOutputTokens || *r.OutputReserveTokens >= r.WindowTokens {
		return fmt.Errorf("context_truncation: invalid output reserve")
	}
	_, err := r.Budget(0)
	return err
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
