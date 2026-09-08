package common

import (
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"testing"
)

// TestCachePresenceRawFacts verifies native JSON and terminal SSE usage before zero-filling converters.
func TestCachePresenceRawFacts(t *testing.T) {
	for _, tc := range []struct {
		name        string
		format      types.RelayFormat
		body, state string
		input       bool
	}{
		{"chat absent", types.RelayFormatOpenAI, `{"usage":{"prompt_tokens":100}}`, "absent", true},
		{"chat zero", types.RelayFormatOpenAI, `{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":0}}}`, "present", true},
		{"responses terminal", types.RelayFormatOpenAIResponses, `{"type":"response.completed","response":{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":0}}}}`, "present", true},
		{"claude start", types.RelayFormatClaude, `{"type":"message_start","message":{"usage":{"input_tokens":100}}}`, "absent", true},
		{"claude null", types.RelayFormatClaude, `{"usage":{"input_tokens":100,"cache_creation_input_tokens":null}}`, "invalid", true},
		{"gemini zero", types.RelayFormatGemini, `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":0}}`, "present", true},
		{"invalid input", types.RelayFormatOpenAI, `{"usage":{"prompt_tokens":1.5}}`, "invalid", false},
		{"no usage", types.RelayFormatOpenAI, `{"choices":[]}`, "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &RelayInfo{CacheUsageSimulation: &CacheUsageState{Presence: "unknown"}}
			info.ObserveCacheUsage(tc.format, []byte(tc.body))
			assert.Equal(t, tc.state, info.CacheUsageSimulation.Presence)
			assert.Equal(t, tc.input, info.CacheUsageSimulation.HasInput)
			if tc.state == "present" || tc.state == "invalid" {
				info.ObserveCacheUsage(tc.format, []byte(`{"usage":{"output_tokens":10}}`))
				assert.Equal(t, tc.state, info.CacheUsageSimulation.Presence)
			}
		})
	}
}
