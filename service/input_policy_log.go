package service

import (
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// AppendInputPolicyLog exposes billing provenance while keeping channel policy details administrator-only.
func AppendInputPolicyLog(target any, info *relaycommon.RelayInfo) {
	if info == nil || (info.ContextTruncation == nil && info.CacheUsageSimulation == nil) {
		return
	}
	other, isLogOther := target.(*model.LogOther)
	values, isMap := target.(map[string]interface{})
	if !isLogOther && !isMap {
		return
	}
	setAdmin := func(key string, value any) {
		if isLogOther {
			other.SetAdmin(key, value)
			return
		}
		admin, _ := values["admin_info"].(map[string]interface{})
		if admin == nil {
			admin = map[string]interface{}{}
			values["admin_info"] = admin
		}
		admin[key] = value
	}
	setPublic := func(key string, value any) {
		if isLogOther {
			other.SetPublic(key, value)
			return
		}
		values[key] = value
	}
	if s := info.ContextTruncation; s != nil {
		setAdmin("context_truncation", s)
		if s.Applied {
			if s.ClientUsageReason == "" {
				s.ClientUsageReason = "usage_missing"
			}
			setPublic("context_truncated", true)
			setPublic("input_tokens_total", s.Billed)
		}
	}
	if s := info.CacheUsageSimulation; s != nil {
		setAdmin("cache_usage_simulation", s)
		if s.Applied {
			setPublic("cache_usage_simulated", true)
			setPublic("input_tokens_total", s.Billed)
		}
	}
}
