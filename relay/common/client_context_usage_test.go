package common

import (
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestClientContextUsageProtocols protects input, total, output and real cache semantics on every wire format.
func TestClientContextUsageProtocols(t *testing.T) {
	tests := []struct {
		name           string
		format         types.RelayFormat
		body, expected string
	}{
		{"online example", types.RelayFormatOpenAI, `{"usage":{"prompt_tokens":114482,"completion_tokens":40,"total_tokens":114522}}`, `{"usage":{"prompt_tokens":275003,"completion_tokens":40,"total_tokens":275043}}`},
		{"converted input alias", types.RelayFormatOpenAI, `{"usage":{"prompt_tokens":100,"input_tokens":100,"completion_tokens":10,"total_tokens":110}}`, `{"usage":{"prompt_tokens":275003,"input_tokens":275003,"completion_tokens":10,"total_tokens":275013}}`},
		{"chat cache", types.RelayFormatOpenAI, `{"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":30,"cached_creation_tokens":20},"completion_tokens_details":{"reasoning_tokens":4}}}`, `{"usage":{"prompt_tokens":275003,"completion_tokens":10,"total_tokens":275013,"prompt_tokens_details":{"cached_tokens":30,"cached_creation_tokens":20},"completion_tokens_details":{"reasoning_tokens":4}}}`},
		{"responses null error", types.RelayFormatOpenAIResponses, `{"error":null,"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110,"input_tokens_details":{"cached_tokens":30}}}`, `{"error":null,"usage":{"input_tokens":275003,"output_tokens":10,"total_tokens":275013,"input_tokens_details":{"cached_tokens":30}}}`},
		{"responses event", types.RelayFormatOpenAIResponses, `{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}}`, `{"type":"response.completed","response":{"usage":{"input_tokens":275003,"output_tokens":10,"total_tokens":275013}}}`},
		{"claude cache", types.RelayFormatClaude, `{"usage":{"input_tokens":50,"output_tokens":10,"cache_read_input_tokens":30,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":15}}}`, `{"usage":{"input_tokens":274953,"output_tokens":10,"cache_read_input_tokens":30,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":15}}}`},
		{"claude start", types.RelayFormatClaude, `{"type":"message_start","message":{"usage":{"input_tokens":50,"output_tokens":0,"cache_read_input_tokens":30,"cache_creation_input_tokens":20}}}`, `{"type":"message_start","message":{"usage":{"input_tokens":274953,"output_tokens":0,"cache_read_input_tokens":30,"cache_creation_input_tokens":20}}}`},
		{"gemini thoughts counted once", types.RelayFormatGemini, `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":20,"cachedContentTokenCount":30,"totalTokenCount":130}}`, `{"usageMetadata":{"promptTokenCount":275003,"candidatesTokenCount":10,"thoughtsTokenCount":20,"cachedContentTokenCount":30,"totalTokenCount":275033}}`},
		{"missing output stays missing", types.RelayFormatOpenAI, `{"usage":{"prompt_tokens":100}}`, `{"usage":{"prompt_tokens":275003}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &ContextTruncationState{Applied: true, Before: 275003}
			original := []byte(tt.body)
			got, changed := RewriteClientContextUsageJSON(original, tt.format, state, 200)
			require.True(t, changed)
			assert.JSONEq(t, tt.expected, string(got))
			assert.Equal(t, tt.body, string(original), "raw provider body remains immutable")
			assert.EqualValues(t, 275003, state.Reported)
			again, changed := RewriteClientContextUsageJSON(got, tt.format, state, 200)
			assert.False(t, changed, "absolute projection is idempotent")
			assert.Equal(t, got, again)
		})
	}
}

// TestClientContextUsageSkipAndBounds keeps errors, invalid counts and absent usage byte-identical.
func TestClientContextUsageSkipAndBounds(t *testing.T) {
	for _, body := range []string{
		`{"usage":null}`, `{"choices":[]}`, `{"usage":{"completion_tokens":10}}`,
		`{"usage":{"prompt_tokens":-1}}`, `{"usage":{"prompt_tokens":1.5}}`,
		`{"usage":{"prompt_tokens":"100"}}`, `{"usage":{"prompt_tokens":2147483648}}`,
		`{"usage":{"prompt_tokens":100,"total_tokens":99}}`,
		`{"usage":{"prompt_tokens":100,"completion_tokens":-1}}`,
		`{"error":{"message":"error"},"usage":{"prompt_tokens":100}}`,
		`{"type":"response.failed","usage":{"prompt_tokens":100}}`,
		`{"status":"failed","usage":{"prompt_tokens":100}}`,
		`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":-1}}}`,
		`{"usage":`,
	} {
		t.Run(body, func(t *testing.T) {
			state := &ContextTruncationState{Applied: true, Before: 1000}
			got, changed := RewriteClientContextUsageJSON([]byte(body), types.RelayFormatOpenAI, state, 200)
			assert.False(t, changed)
			assert.Equal(t, body, string(got))
			assert.Zero(t, state.Reported)
		})
	}
	for _, status := range []int{400, 401, 500} {
		state := &ContextTruncationState{Applied: true, Before: 1000}
		body := []byte(`{"usage":{"prompt_tokens":100}}`)
		got, changed := RewriteClientContextUsageJSON(body, types.RelayFormatOpenAI, state, status)
		assert.False(t, changed)
		assert.Equal(t, body, got)
	}
	state := &ContextTruncationState{Before: 1000}
	body := []byte(`{"usage":{"prompt_tokens":100}}`)
	got, changed := RewriteClientContextUsageJSON(body, types.RelayFormatOpenAI, state, 200)
	assert.False(t, changed)
	assert.Equal(t, body, got)
}

// TestClientContextUsageSparseClaude preserves output-only deltas and incorporates later real cache counters.
func TestClientContextUsageSparseClaude(t *testing.T) {
	state := &ContextTruncationState{Applied: true, Before: 1000}
	_, changed := RewriteClientContextUsageJSON([]byte(`{"message":{"usage":{"input_tokens":50,"cache_read_input_tokens":100}}}`), types.RelayFormatClaude, state, 200)
	require.True(t, changed)
	delta := []byte(`{"type":"message_delta","usage":{"output_tokens":20}}`)
	got, changed := RewriteClientContextUsageJSON(delta, types.RelayFormatClaude, state, 200)
	assert.False(t, changed)
	assert.Equal(t, delta, got)
	got, changed = RewriteClientContextUsageJSON([]byte(`{"type":"message_delta","usage":{"output_tokens":25,"cache_creation_input_tokens":200}}`), types.RelayFormatClaude, state, 200)
	require.True(t, changed)
	assert.JSONEq(t, `{"type":"message_delta","usage":{"output_tokens":25,"cache_creation_input_tokens":200,"input_tokens":700}}`, string(got))
	assert.EqualValues(t, 1000, state.Reported)
	// A provider estimate above the local estimate is never reduced, even with sparse cache updates.
	got, _ = RewriteClientContextUsageJSON([]byte(`{"usage":{"input_tokens":1500,"output_tokens":30}}`), types.RelayFormatClaude, state, 200)
	assert.EqualValues(t, 1500, gjson.GetBytes(got, "usage.input_tokens").Int())
	assert.EqualValues(t, 1800, state.Reported)
}

// TestClientContextUsageFreshRequests verifies growth before client compaction and normal counts afterwards.
func TestClientContextUsageFreshRequests(t *testing.T) {
	for _, before := range []int{250000, 275003, 280000} {
		state := &ContextTruncationState{Applied: true, Before: before}
		got, _ := RewriteClientContextUsageJSON([]byte(`{"usage":{"prompt_tokens":114482}}`), types.RelayFormatOpenAI, state, 200)
		assert.EqualValues(t, before, gjson.GetBytes(got, "usage.prompt_tokens").Int())
	}
	state := &ContextTruncationState{Before: 9000}
	body := []byte(`{"usage":{"prompt_tokens":9500}}`)
	got, changed := RewriteClientContextUsageJSON(body, types.RelayFormatOpenAI, state, 200)
	assert.False(t, changed)
	assert.Equal(t, body, got)
}

// TestClientContextUsageKeepsKnownInputWithinAttempt protects real input above the local estimate across sparse frames.
func TestClientContextUsageKeepsKnownInputWithinAttempt(t *testing.T) {
	state := &ContextTruncationState{Applied: true, Before: 1000}
	_, _ = RewriteClientContextUsageJSON([]byte(`{"usage":{"prompt_tokens":2000,"total_tokens":2000}}`), types.RelayFormatOpenAI, state, 200)
	got, changed := RewriteClientContextUsageJSON([]byte(`{"usage":{"prompt_tokens":0,"completion_tokens":10,"total_tokens":10}}`), types.RelayFormatOpenAI, state, 200)
	require.True(t, changed)
	assert.JSONEq(t, `{"usage":{"prompt_tokens":2000,"completion_tokens":10,"total_tokens":2010}}`, string(got))
}
