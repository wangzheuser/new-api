package service

import relaycommon "github.com/QuantumNous/new-api/relay/common"

// AppendInputPolicyLog exposes billing provenance while keeping channel policy details administrator-only.
func AppendInputPolicyLog(other map[string]interface{}, info *relaycommon.RelayInfo) {
	if info == nil || (info.ContextTruncation == nil && info.CacheUsageSimulation == nil) {
		return
	}
	admin, _ := other["admin_info"].(map[string]interface{})
	if admin == nil {
		admin = map[string]interface{}{}
		other["admin_info"] = admin
	}
	if s := info.ContextTruncation; s != nil {
		admin["context_truncation"] = s
		if s.Applied {
			other["context_truncated"] = true
			other["input_tokens_total"] = s.Billed
		}
	}
	if s := info.CacheUsageSimulation; s != nil {
		admin["cache_usage_simulation"] = s
		if s.Applied {
			other["cache_usage_simulated"] = true
			other["input_tokens_total"] = s.Billed
		}
	}
}
