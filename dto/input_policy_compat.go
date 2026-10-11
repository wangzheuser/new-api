package dto

import (
	"fmt"
	"github.com/tidwall/gjson"
	"strings"
)

func validateChannelInputPolicies(s ChannelOtherSettings, raw string) error {
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
