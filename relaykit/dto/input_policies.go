package dto

import (
	"fmt"
	"math"
	"strings"

	"github.com/tidwall/gjson"
)

const MaxOutputTokens = math.MaxInt32 / 2

type ContextTruncationRule struct {
	Mode                string `json:"mode"`
	WindowTokens        int    `json:"window_tokens,omitempty"`
	OutputReserveTokens *int   `json:"output_reserve_tokens"`
}

type ContextTruncationPolicy struct {
	ForceDisabled bool                             `json:"force_disabled,omitempty"`
	Models        map[string]ContextTruncationRule `json:"models"`
}

func (r ContextTruncationRule) Threshold() int { return 90 }
func (r ContextTruncationRule) Keep() int      { return 1 }
func (r ContextTruncationRule) Safety() int {
	return min(int((int64(r.WindowTokens)*2+99)/100), 8192)
}
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
func (r ContextTruncationRule) Validate(channel bool) error {
	if r.Mode == "off" || (channel && (r.Mode == "inherit" || r.Mode == "")) {
		return nil
	}
	if r.Mode != "custom" || r.WindowTokens <= 0 || int64(r.WindowTokens) > math.MaxInt32 {
		return fmt.Errorf("context_truncation: invalid rule")
	}
	if r.OutputReserveTokens == nil || *r.OutputReserveTokens <= 0 || *r.OutputReserveTokens > MaxOutputTokens || *r.OutputReserveTokens >= r.WindowTokens {
		return fmt.Errorf("context_truncation: invalid output reserve")
	}
	_, err := r.Budget(0)
	return err
}
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

type CacheUsageSimulationPolicy struct {
	ForceDisabled          bool   `json:"force_disabled,omitempty"`
	Enabled                bool   `json:"enabled,omitempty"`
	Mode                   string `json:"mode,omitempty"`
	CreationTriggerPercent *int   `json:"creation_trigger_percent,omitempty"`
	ReadTriggerPercent     *int   `json:"read_trigger_percent,omitempty"`
	CreationTokenPercent   *int   `json:"creation_token_percent,omitempty"`
	ReadTokenPercent       *int   `json:"read_token_percent,omitempty"`
}

func (p CacheUsageSimulationPolicy) Percentages() [4]int {
	v := [4]int{20, 60, 30, 50}
	for i, n := range []*int{p.CreationTriggerPercent, p.ReadTriggerPercent, p.CreationTokenPercent, p.ReadTokenPercent} {
		if n != nil {
			v[i] = *n
		}
	}
	return v
}
func (p CacheUsageSimulationPolicy) Validate(channel bool) error {
	if channel && (p.ForceDisabled || p.Enabled || (p.Mode != "" && p.Mode != "inherit" && p.Mode != "off" && p.Mode != "custom")) {
		return fmt.Errorf("cache_usage_simulation: invalid channel mode")
	}
	if !channel && p.Mode != "" {
		return fmt.Errorf("cache_usage_simulation: global mode is not supported")
	}
	v := p.Percentages()
	for _, n := range v {
		if n < 0 || n > 100 {
			return fmt.Errorf("cache_usage_simulation: percentages must be integers between 0 and 100")
		}
	}
	if v[0] == 100 && v[1] == 100 || v[2]+v[3] > 100 {
		return fmt.Errorf("cache_usage_simulation: invalid percentages")
	}
	return nil
}
func SplitCacheUsage(total int, p CacheUsageSimulationPolicy, samples [2]int) (creation, read int) {
	if total <= 0 || int64(total) > math.MaxInt32 || p.Validate(false) != nil {
		return 0, 0
	}
	v := p.Percentages()
	if samples[0] >= 0 && samples[0] < v[0] {
		creation = int(int64(total) * int64(v[2]) / 100)
	}
	if samples[1] >= 0 && samples[1] < v[1] {
		read = int(int64(total) * int64(v[3]) / 100)
	}
	return
}

func (s ChannelOtherSettings) ValidateInputPolicies(raw string) error {
	for _, key := range []string{"context_truncation", "cache_usage_simulation"} {
		v := gjson.Get(raw, key)
		if v.Exists() && !v.IsObject() {
			return fmt.Errorf("input policies: policy must be an object")
		}
	}
	if s.ContextTruncation == nil && s.CacheUsageSimulation == nil {
		return nil
	}
	if len(raw) > 65535 {
		return fmt.Errorf("input policies: channel settings exceed 64 KiB")
	}
	if s.ContextTruncation != nil {
		if err := s.ContextTruncation.Validate(true); err != nil {
			return err
		}
	}
	if s.CacheUsageSimulation != nil {
		for _, key := range []string{"creation_trigger_percent", "read_trigger_percent", "creation_token_percent", "read_token_percent"} {
			v := gjson.Get(raw, "cache_usage_simulation."+key)
			if v.Exists() && strings.TrimSpace(v.Raw) == "null" {
				return fmt.Errorf("cache_usage_simulation: null percentage")
			}
		}
		return s.CacheUsageSimulation.Validate(true)
	}
	return nil
}
