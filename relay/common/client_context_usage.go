package common

import (
	"math"

	"github.com/QuantumNous/new-api/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// clientContextUsage retains only sparse Claude counters within one attempt.
type clientContextUsage struct {
	input, read, creation, creation5m, creation1h int64
	hasInput                                      bool
}

// RewriteClientContextUsageJSON restores the client's logical context estimate without touching billing usage.
func RewriteClientContextUsageJSON(data []byte, format types.RelayFormat, state *ContextTruncationState, status int) ([]byte, bool) {
	if state == nil || !state.Applied || state.Before <= 0 {
		return data, false
	}
	if state.ClientUsageReason == "" {
		state.ClientUsageReason = "usage_missing"
	}
	if status < 200 || status >= 300 || !gjson.ValidBytes(data) {
		return data, false
	}
	root := gjson.ParseBytes(data)
	// Null error fields are normal in successful Responses payloads.
	for _, path := range []string{"error", "response.error"} {
		if value := root.Get(path); value.Exists() && value.Type != gjson.Null {
			return data, false
		}
	}
	switch root.Get("type").String() {
	case "error", "response.failed", "response.cancelled":
		return data, false
	}
	for _, path := range []string{"status", "response.status"} {
		if value := root.Get(path).String(); value == "failed" || value == "cancelled" {
			return data, false
		}
	}

	path, inputKey, totalKey := "usage", "prompt_tokens", "total_tokens"
	inputAlias, outputKey := "input_tokens", "completion_tokens"
	cacheKeys := []string{"prompt_tokens_details.cached_tokens", "prompt_tokens_details.cached_creation_tokens", "prompt_tokens_details.cache_write_tokens"}
	switch format {
	case types.RelayFormatOpenAI:
	case types.RelayFormatOpenAIResponses:
		inputKey = "input_tokens"
		inputAlias, outputKey = "prompt_tokens", "output_tokens"
		cacheKeys = []string{"input_tokens_details.cached_tokens", "input_tokens_details.cached_creation_tokens", "input_tokens_details.cache_write_tokens"}
		if root.Get("response").IsObject() {
			path = "response.usage"
		}
	case types.RelayFormatClaude:
		inputKey, totalKey = "input_tokens", ""
		inputAlias, outputKey = "", "output_tokens"
		cacheKeys = []string{"cache_read_input_tokens", "cache_creation_input_tokens", "cache_creation.ephemeral_5m_input_tokens", "cache_creation.ephemeral_1h_input_tokens"}
		if root.Get("message").IsObject() {
			path = "message.usage"
		}
	case types.RelayFormatGemini:
		path, inputKey, totalKey = "usageMetadata", "promptTokenCount", "totalTokenCount"
		inputAlias, outputKey = "", "candidatesTokenCount"
		cacheKeys = []string{"cachedContentTokenCount"}
	default:
		state.ClientUsageReason = "unsupported_response_format"
		return data, false
	}
	usage := root.Get(path)
	if !usage.IsObject() {
		return data, false
	}
	input := usage.Get(inputKey)
	keys := append([]string{inputKey, outputKey}, cacheKeys...)
	if inputAlias != "" {
		keys = append(keys, inputAlias)
	}
	if totalKey != "" {
		keys = append(keys, totalKey)
	}
	// Match the existing usage boundary; malformed upstream counters stay untouched.
	for _, key := range keys {
		if value := usage.Get(key); value.Exists() && !validClientContextCounter(value) {
			state.ClientUsageReason = "invalid_usage"
			return data, false
		}
	}

	rawInput := input.Int()
	cache := int64(0)
	if format == types.RelayFormatClaude {
		pending := state.clientUsage
		if input.Exists() {
			pending.input, pending.hasInput = rawInput, true
		}
		fields := []*int64{&pending.read, &pending.creation, &pending.creation5m, &pending.creation1h}
		cacheChanged := false
		for i, key := range cacheKeys {
			if value := usage.Get(key); value.Exists() {
				*fields[i] = value.Int()
				cacheChanged = true
			}
		}
		state.clientUsage = pending
		// Output-only deltas must not repeat or accumulate the restored input.
		if !pending.hasInput || (!input.Exists() && !cacheChanged) {
			return data, false
		}
		rawInput = pending.input
		cache = pending.read + max(pending.creation, pending.creation5m+pending.creation1h)
	} else {
		if !input.Exists() {
			if state.Reported == 0 {
				state.ClientUsageReason = "input_usage_missing"
			}
			return data, false
		}
		cache = usage.Get(cacheKeys[0]).Int()
		if len(cacheKeys) == 3 {
			cache += max(usage.Get(cacheKeys[1]).Int(), usage.Get(cacheKeys[2]).Int())
		}
	}
	rawTotal := rawInput
	if format == types.RelayFormatClaude {
		rawTotal += cache
	} else if inputAlias != "" {
		rawTotal = max(rawTotal, usage.Get(inputAlias).Int())
	}
	// Later sparse/default-filled frames must not lower an already observed input total in this attempt.
	reported := max(int64(state.Before), rawTotal, cache, state.Reported)
	newInput := reported
	if format == types.RelayFormatClaude {
		newInput -= cache
	}
	result := data
	var err error
	if !input.Exists() || input.Int() != newInput {
		result, err = sjson.SetBytes(result, path+"."+inputKey, newInput)
		if err != nil {
			return data, false
		}
	}
	// Some existing converters emit both OpenAI input aliases; keep them consistent without adding fields.
	if inputAlias != "" {
		if alias := usage.Get(inputAlias); alias.Exists() && alias.Int() != newInput {
			result, err = sjson.SetBytes(result, path+"."+inputAlias, newInput)
			if err != nil {
				return data, false
			}
		}
	}
	// Preserve the provider's output/thought accounting by changing only the input contribution.
	if totalKey != "" {
		if total := usage.Get(totalKey); total.Exists() {
			if total.Int() < rawInput {
				state.ClientUsageReason = "invalid_usage"
				return data, false
			}
			if newTotal := total.Int() - rawInput + newInput; newTotal != total.Int() {
				result, err = sjson.SetBytes(result, path+"."+totalKey, newTotal)
				if err != nil {
					return data, false
				}
			}
		}
	}
	state.Reported = reported
	state.ClientUsageReason = "logical_input_reported"
	return result, string(result) != string(data)
}

// validClientContextCounter rejects negative, fractional and overflowing upstream counts.
func validClientContextCounter(value gjson.Result) bool {
	return value.Type == gjson.Number && value.Float() >= 0 && value.Float() <= math.MaxInt32 && math.Trunc(value.Float()) == value.Float()
}
