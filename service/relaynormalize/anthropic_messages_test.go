package relaynormalize

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeToolIDNormalizerDeterministicContract(t *testing.T) {
	normalizer := NewClaudeToolIDNormalizer()
	tests := []struct {
		name      string
		original  string
		expected  string
		changed   bool
		collision bool
	}{
		{name: "legal", original: "call_ABC-123", expected: "call_ABC-123"},
		{name: "continuous invalid run", original: "call.:bad", expected: "call_bad", changed: true},
		{name: "empty", original: "", expected: "toolu_e3b0c44298fc", changed: true},
		{name: "all invalid", original: "!!!", expected: "_", changed: true},
		{name: "collision base", original: "a:b", expected: "a_b", changed: true},
		{name: "collision suffix", original: "a?b", expected: "a_b_c2a7b64a", changed: true, collision: true},
		{name: "repeated original", original: "a?b", expected: "a_b_c2a7b64a", changed: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, changed, collision := normalizer.Normalize(test.original)
			assert.Equal(t, test.expected, actual)
			assert.Equal(t, test.changed, changed)
			assert.Equal(t, test.collision, collision)
		})
	}
	assert.Equal(t, 1, normalizer.Collisions())
}

func TestNormalizeAnthropicMessagesCompatibleDropsReasoningAndSynchronizesToolIDs(t *testing.T) {
	body := []byte(`{
      "model":"MODEL_X",
      "metadata":{"large":90071992547409931234},
      "messages":[
		{"role":"assistant","content":[]},
        {"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"redacted_thinking","data":"opaque"}]},
        {"role":"assistant","content":[{"type":"thinking","thinking":"kept"},{"type":"text","text":"visible"},{"type":"tool_use","id":"a:b","name":"lookup","input":{"q":"x"}}]},
        {"role":"user","content":[{"type":"tool_result","tool_use_id":"a:b","content":"one"}]},
        {"role":"assistant","content":[{"type":"tool_use","id":"a?b","name":"lookup","input":{"q":"y"}}]},
        {"role":"user","content":[{"type":"tool_result","tool_use_id":"a?b","content":"two"},{"type":"tool_result","tool_use_id":"missing:id","content":"orphan"}]}
      ]
    }`)

	normalized, audit, err := NormalizeRequestByID(RequestNormalizerAnthropicMessagesCompatible, body)
	require.NoError(t, err)
	require.NoError(t, ValidateRequestByID(RequestNormalizerAnthropicMessagesCompatible, normalized))
	assert.Equal(t, types.ProtocolNormalizationAudit{
		Normalizer:                          RequestNormalizerAnthropicMessagesCompatible,
		ReasoningAssistantMessagesPreserved: 1,
		ReasoningOnlyAssistantDropped:       1,
		EmptyAssistantMessagesDropped:       1,
		ToolIDsNormalized:                   5,
		ToolIDCollisions:                    1,
		OrphanToolResultIDs:                 1,
	}, audit)
	assert.Contains(t, string(normalized), `90071992547409931234`)
	assert.NotContains(t, string(normalized), `"thinking":"hidden"`)
	assert.Contains(t, string(normalized), `"thinking":"kept"`)

	var request struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, common.Unmarshal(normalized, &request))
	require.Len(t, request.Messages, 4)
	assert.Equal(t, "a_b", request.Messages[0].Content[2]["id"])
	assert.Equal(t, "a_b", request.Messages[1].Content[0]["tool_use_id"])
	assert.Equal(t, "a_b_c2a7b64a", request.Messages[2].Content[0]["id"])
	assert.Equal(t, "a_b_c2a7b64a", request.Messages[3].Content[0]["tool_use_id"])
	assert.Equal(t, "missing_id", request.Messages[3].Content[1]["tool_use_id"])
}

func TestNormalizeAnthropicMessagesCompatibleDropsStructurallyBlankAssistantContent(t *testing.T) {
	body := []byte(`{
      "messages":[
        {"role":"assistant","content":""},
        {"role":"assistant","content":"   "},
        {"role":"assistant","content":null},
        {"role":"assistant"},
        {"role":"assistant","content":[{"type":"text","text":""}]},
        {"role":"assistant","content":[{"type":"input_text","text":"  "}]},
        {"role":"assistant","content":[{"type":"text","text":null}]},
        {"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"  "}]},
        {"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"},{"type":"input_text","text":""}]},
        {"role":"assistant","content":[{"type":"thinking","thinking":"kept"},{"type":"text","text":"visible"},{"type":"text","text":""}]},
        {"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]},
        {"role":"user","content":"question"}
      ]
    }`)

	normalized, audit, err := NormalizeRequestByID(RequestNormalizerAnthropicMessagesCompatible, body)
	require.NoError(t, err)
	require.NoError(t, ValidateRequestByID(RequestNormalizerAnthropicMessagesCompatible, normalized))
	assert.Equal(t, 7, audit.EmptyAssistantMessagesDropped)
	assert.Equal(t, 2, audit.ReasoningOnlyAssistantDropped)
	assert.Equal(t, 1, audit.ReasoningAssistantMessagesPreserved)

	var root map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(normalized, &root))
	var messages []json.RawMessage
	require.NoError(t, common.Unmarshal(root["messages"], &messages))
	assert.Len(t, messages, 3)
	assert.Contains(t, string(normalized), `"text":"visible"`)
	assert.Contains(t, string(normalized), `"id":"call_1"`)
}

func TestNormalizeAnthropicMessagesCompatibleHandlesBoundaryIDs(t *testing.T) {
	body := []byte(`{
      "messages":[
        {"role":"assistant","content":[
          {"type":"tool_use","id":"","name":"empty","input":{}},
          {"type":"tool_use","id":"!!!","name":"invalid","input":{}},
          {"type":"tool_use","id":"same:id","name":"repeat","input":{}},
          {"type":"tool_use","id":"same:id","name":"repeat","input":{}}
        ]},
        {"role":"user","content":[
          {"type":"tool_result","tool_use_id":"","content":"empty"},
          {"type":"tool_result","tool_use_id":"!!!","content":"invalid"},
          {"type":"tool_result","tool_use_id":"same:id","content":"repeat"}
        ]}
      ]
    }`)

	normalized, audit, err := NormalizeRequestByID(RequestNormalizerAnthropicMessagesCompatible, body)
	require.NoError(t, err)
	require.NoError(t, ValidateRequestByID(RequestNormalizerAnthropicMessagesCompatible, normalized))
	assert.Equal(t, 7, audit.ToolIDsNormalized)
	assert.Zero(t, audit.ToolIDCollisions)
	assert.Zero(t, audit.OrphanToolResultIDs)
	assert.Contains(t, string(normalized), `"id":"toolu_e3b0c44298fc"`)
	assert.Contains(t, string(normalized), `"id":"_"`)
	assert.Contains(t, string(normalized), `"id":"same_id"`)
}

func TestNormalizeAnthropicMessagesCompatiblePreservesVisibleAssistantVariants(t *testing.T) {
	body := []byte(`{
      "messages":[
        {"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"visible"}]},
        {"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"},{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]},
        {"role":"assistant","content":[{"type":"custom_visible","value":1},{"type":"thinking","thinking":"hidden"}]},
        {"role":"user","content":"question"}
      ]
    }`)

	normalized, audit, err := NormalizeRequestByID(RequestNormalizerAnthropicMessagesCompatible, body)
	require.NoError(t, err)
	require.NoError(t, ValidateRequestByID(RequestNormalizerAnthropicMessagesCompatible, normalized))
	assert.Equal(t, 3, audit.ReasoningAssistantMessagesPreserved)
	assert.Zero(t, audit.ReasoningOnlyAssistantDropped)
	assert.Zero(t, audit.ToolIDsNormalized)
	var root map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(normalized, &root))
	var messages []json.RawMessage
	require.NoError(t, common.Unmarshal(root["messages"], &messages))
	assert.Len(t, messages, 4)
}

func TestNormalizeAnthropicMessagesCompatibleRejectsUnknownNormalizerAndInvalidIDs(t *testing.T) {
	_, audit, err := NormalizeRequestByID("missing", []byte(`{}`))
	require.ErrorContains(t, err, "not registered")
	assert.Equal(t, "missing", audit.Normalizer)
	require.ErrorContains(t, ValidateRequestByID("missing", []byte(`{}`)), "not registered")
	require.ErrorContains(t, validateAnthropicMessagesCompatible([]byte(`{
      "messages":[{"role":"assistant","content":[{"type":"tool_use","id":"bad:id"}]}]
    }`)), "does not match")
	require.ErrorContains(t, validateAnthropicMessagesCompatible([]byte(`{
      "messages":[{"role":"assistant","content":[]}]
    }`)), "empty assistant message")
	require.ErrorContains(t, validateAnthropicMessagesCompatible([]byte(`{
      "messages":[{"role":"assistant","content":"  "}]
    }`)), "empty assistant message")
	require.ErrorContains(t, validateAnthropicMessagesCompatible([]byte(`{
      "messages":[{"role":"assistant","content":[{"type":"text","text":""}]}]
    }`)), "empty assistant message")
	require.ErrorContains(t, validateAnthropicMessagesCompatible([]byte(`{
      "messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":" "}]}]
    }`)), "reasoning-only assistant message")
}
