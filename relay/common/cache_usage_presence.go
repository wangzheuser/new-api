package common

import (
	"github.com/QuantumNous/new-api/types"
	"github.com/tidwall/gjson"
	"math"
)

// ObserveCacheUsage reads raw provider fields before conversion, augmentation or zero filling.
func (info *RelayInfo) ObserveCacheUsage(format types.RelayFormat, body []byte) {
	if info == nil || info.CacheUsageSimulation == nil {
		return
	}
	s := info.CacheUsageSimulation
	if s.Reason != "" || len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	root := gjson.ParseBytes(body)
	var usages []gjson.Result
	paths := []string{}
	switch format {
	case types.RelayFormatOpenAI:
		usages = append(usages, root.Get("usage"))
		paths = []string{"prompt_tokens_details.cached_tokens", "prompt_tokens_details.cache_write_tokens", "prompt_tokens_details.cached_creation_tokens", "input_tokens_details.cached_tokens", "cached_tokens", "prompt_cache_hit_tokens"}
		for _, choice := range root.Get("choices").Array() {
			usages = append(usages, choice.Get("usage"))
		}
		if v := root.Get("timings.cache_n"); v.Exists() {
			observeCacheField(s, v)
		}
	case types.RelayFormatOpenAIResponses:
		usages = []gjson.Result{root.Get("usage"), root.Get("response.usage")}
		paths = []string{"input_tokens_details.cached_tokens", "input_tokens_details.cache_write_tokens", "input_tokens_details.cached_creation_tokens"}
	case types.RelayFormatClaude:
		usages = []gjson.Result{root.Get("usage"), root.Get("message.usage")}
		paths = []string{"cache_read_input_tokens", "cache_creation_input_tokens", "claude_cache_creation_5_m_tokens", "claude_cache_creation_1_h_tokens"}
	case types.RelayFormatGemini:
		usages = []gjson.Result{root.Get("usageMetadata")}
		paths = []string{"cachedContentTokenCount"}
	default:
		return
	}
	for _, u := range usages {
		if !u.IsObject() {
			continue
		}
		if s.Presence == "unknown" {
			s.Presence = "absent"
		}
		for _, key := range []string{"prompt_tokens", "input_tokens", "promptTokenCount"} {
			v := u.Get(key)
			if v.Exists() {
				if v.Type != gjson.Number || v.Float() < 0 || math.Trunc(v.Float()) != v.Float() || v.Float() > 2147483647 {
					s.Presence = "invalid"
				} else if v.Int() > 0 {
					s.HasInput = true
				}
			}
		}
		for _, path := range paths {
			if v := u.Get(path); v.Exists() {
				observeCacheField(s, v)
			}
		}
		if format == types.RelayFormatClaude {
			if v := u.Get("cache_creation"); v.Exists() {
				if !v.IsObject() {
					s.Presence = "invalid"
				} else {
					if s.Presence != "invalid" {
						s.Presence = "present"
					}
					v.ForEach(func(_, n gjson.Result) bool { observeCacheField(s, n); return true })
				}
			}
		}
	}
}

// observeCacheField keeps explicit zero distinct from absence and never clears an invalid observation.
func observeCacheField(s *CacheUsageState, v gjson.Result) {
	if v.Type != gjson.Number || v.Float() < 0 || math.Trunc(v.Float()) != v.Float() || v.Float() > 2147483647 {
		s.Presence = "invalid"
		return
	}
	if s.Presence != "invalid" {
		s.Presence = "present"
	}
}
