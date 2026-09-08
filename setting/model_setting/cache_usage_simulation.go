package model_setting

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/tidwall/gjson"
	"strings"
	"sync/atomic"
)

const CacheUsageSimulationOption = "cache_usage_simulation.policy"

var cacheSimulationPolicy atomic.Pointer[dto.CacheUsageSimulationPolicy]

// ValidateInputPolicyOption validates supported option keys before any database write.
func ValidateInputPolicyOption(key, value string) error {
	switch key {
	case ContextTruncationOption:
		_, err := ParseContextTruncation(value)
		return err
	case CacheUsageSimulationOption:
		_, err := ParseCacheUsageSimulation(value)
		return err
	}
	return nil
}

// ParseCacheUsageSimulation rejects null percentages rather than silently replacing them with defaults.
func ParseCacheUsageSimulation(value string) (*dto.CacheUsageSimulationPolicy, error) {
	if len(value) > 65535 {
		return nil, fmt.Errorf("cache_usage_simulation: policy exceeds 64 KiB")
	}
	var p *dto.CacheUsageSimulationPolicy
	if err := common.UnmarshalJsonStr(value, &p); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("cache_usage_simulation: policy must be an object")
	}
	for _, key := range []string{"creation_trigger_percent", "read_trigger_percent", "creation_token_percent", "read_token_percent"} {
		v := gjson.Get(value, key)
		if v.Exists() && strings.TrimSpace(v.Raw) == "null" {
			return nil, fmt.Errorf("cache_usage_simulation: null percentage")
		}
	}
	return p, p.Validate(false)
}

// SetCacheUsageSimulation publishes only a fully parsed immutable configuration.
func SetCacheUsageSimulation(value string) error {
	p, err := ParseCacheUsageSimulation(value)
	if err != nil {
		cacheSimulationPolicy.Store(&dto.CacheUsageSimulationPolicy{ForceDisabled: true})
		return err
	}
	cacheSimulationPolicy.Store(p)
	return nil
}

// ResolveCacheUsageSimulation separates global defaults from a site-wide stop.
func ResolveCacheUsageSimulation(channel *dto.CacheUsageSimulationPolicy) (dto.CacheUsageSimulationPolicy, string, bool) {
	p := cacheSimulationPolicy.Load()
	if p != nil && p.ForceDisabled {
		return dto.CacheUsageSimulationPolicy{}, "force_disabled", false
	}
	if channel != nil && channel.Mode != "" && channel.Mode != "inherit" {
		v := *channel
		enabled := v.Mode == "custom" && v.Validate(true) == nil
		v.Mode = ""
		return v, "channel", enabled
	}
	if p != nil {
		return *p, "global", p.Enabled
	}
	return dto.CacheUsageSimulationPolicy{}, "disabled", false
}
