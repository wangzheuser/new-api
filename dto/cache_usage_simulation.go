package dto

import (
	"fmt"
	"github.com/tidwall/gjson"
	"strings"
)

// CacheUsageSimulationPolicy keeps missing numeric fields distinct from explicit zero.
type CacheUsageSimulationPolicy struct {
	ForceDisabled          bool   `json:"force_disabled,omitempty"`
	Enabled                bool   `json:"enabled,omitempty"`
	Mode                   string `json:"mode,omitempty"`
	CreationTriggerPercent *int   `json:"creation_trigger_percent,omitempty"`
	ReadTriggerPercent     *int   `json:"read_trigger_percent,omitempty"`
	CreationTokenPercent   *int   `json:"creation_token_percent,omitempty"`
	ReadTokenPercent       *int   `json:"read_token_percent,omitempty"`
}

// Percentages returns creation/read probabilities followed by their token shares.
func (p CacheUsageSimulationPolicy) Percentages() [4]int {
	values := [4]int{20, 60, 30, 50}
	for i, v := range []*int{p.CreationTriggerPercent, p.ReadTriggerPercent, p.CreationTokenPercent, p.ReadTokenPercent} {
		if v != nil {
			values[i] = *v
		}
	}
	return values
}

// Validate rejects impossible splits and accidental always-both activation.
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
	if v[0] == 100 && v[1] == 100 {
		return fmt.Errorf("cache_usage_simulation: both trigger probabilities cannot be 100")
	}
	if v[2]+v[3] > 100 {
		return fmt.Errorf("cache_usage_simulation: token percentages cannot exceed 100 in total")
	}
	return nil
}

// SplitCacheUsage is a deterministic, allocation-free classification, not a cache lookup.
func SplitCacheUsage(total int, p CacheUsageSimulationPolicy, samples [2]int) (creation, read int) {
	if total <= 0 || int64(total) > 2147483647 || p.Validate(false) != nil {
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

// ValidateInputPolicies checks channel-only scope and rejects explicit null probabilities.
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
