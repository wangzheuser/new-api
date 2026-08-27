package relaynormalize

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIResponsesCompatibleKeepsCallReferencesPaired(t *testing.T) {
	body := []byte(`{
		"model":"MODEL_X",
		"metadata":{"sequence":90071992547409931234},
		"input":[
			{"type":"function_call","call_id":"call:a","name":"lookup","arguments":"{}"},
			{"type":"function_call_output","call_id":"call:a","output":"one"},
			{"type":"custom_tool_call","call_id":"call.a","name":"custom","input":"x"},
			{"type":"custom_tool_call_output","call_id":"call.a","output":"two"},
			{"type":"function_call_output","call_id":"missing:id","output":"orphan"},
			{"type":"computer_call","call_id":"computer:id","action":{"type":"screenshot"}},
			{"type":"computer_call_output","call_id":"computer:id","output":{"type":"computer_screenshot"}},
			{"role":"user","content":"keep"}
		]
	}`)

	normalized, audit, err := NormalizeRequestByID(RequestNormalizerOpenAIResponsesCompatible, body)
	require.NoError(t, err)
	require.NoError(t, ValidateRequestByID(RequestNormalizerOpenAIResponsesCompatible, normalized))
	assert.Equal(t, types.ProtocolNormalizationAudit{
		Normalizer:          RequestNormalizerOpenAIResponsesCompatible,
		ToolIDsNormalized:   7,
		ToolIDCollisions:    1,
		OrphanToolResultIDs: 1,
	}, audit)
	assert.Contains(t, string(normalized), `90071992547409931234`)

	var request struct {
		Input []map[string]json.RawMessage `json:"input"`
	}
	require.NoError(t, common.Unmarshal(normalized, &request))
	assert.Equal(t, "call_a", responseTestString(t, request.Input[0]["call_id"]))
	assert.Equal(t, "call_a", responseTestString(t, request.Input[1]["call_id"]))
	assert.Equal(t, "call_a_c65a0dc8", responseTestString(t, request.Input[2]["call_id"]))
	assert.Equal(t, "call_a_c65a0dc8", responseTestString(t, request.Input[3]["call_id"]))
	assert.Equal(t, "missing_id", responseTestString(t, request.Input[4]["call_id"]))
	assert.Equal(t, "computer_id", responseTestString(t, request.Input[5]["call_id"]))
	assert.Equal(t, "computer_id", responseTestString(t, request.Input[6]["call_id"]))
	assert.Equal(t, "keep", responseTestString(t, request.Input[7]["content"]))
}

func TestNormalizeOpenAIResponsesCompatibleHandlesEmptyAndLegalIDs(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"function_call","call_id":"","name":"empty","arguments":"{}"},
		{"type":"function_call_output","call_id":"","output":"empty"},
		{"type":"function_call","call_id":"call_legal-1","name":"legal","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_legal-1","output":"legal"}
	]}`)

	normalized, audit, err := NormalizeRequestByID(RequestNormalizerOpenAIResponsesCompatible, body)
	require.NoError(t, err)
	require.NoError(t, ValidateRequestByID(RequestNormalizerOpenAIResponsesCompatible, normalized))
	assert.Equal(t, 2, audit.ToolIDsNormalized)
	assert.Zero(t, audit.ToolIDCollisions)
	assert.Zero(t, audit.OrphanToolResultIDs)

	var request struct {
		Input []map[string]json.RawMessage `json:"input"`
	}
	require.NoError(t, common.Unmarshal(normalized, &request))
	assert.Equal(t, "toolu_e3b0c44298fc", responseTestString(t, request.Input[0]["call_id"]))
	assert.Equal(t, "toolu_e3b0c44298fc", responseTestString(t, request.Input[1]["call_id"]))
	assert.Equal(t, "call_legal-1", responseTestString(t, request.Input[2]["call_id"]))
	assert.Equal(t, "call_legal-1", responseTestString(t, request.Input[3]["call_id"]))
}

func TestNormalizeOpenAIResponsesCompatibleLeavesStringInputUnchanged(t *testing.T) {
	body := []byte(`{"model":"MODEL_X","input":"hello"}`)

	normalized, audit, err := NormalizeRequestByID(RequestNormalizerOpenAIResponsesCompatible, body)
	require.NoError(t, err)
	assert.Equal(t, body, normalized)
	assert.Equal(t, types.ProtocolNormalizationAudit{Normalizer: RequestNormalizerOpenAIResponsesCompatible}, audit)
	require.NoError(t, ValidateRequestByID(RequestNormalizerOpenAIResponsesCompatible, normalized))
}

func TestNormalizeOpenAIResponsesCompatibleDropsStructurallyEmptyAssistantItems(t *testing.T) {
	body := []byte(`{
		"model":"MODEL_X",
		"input":[
			{"role":"user","content":"keep user"},
			{"role":"assistant","content":""},
			{"role":"assistant","content":"  \n\t "},
			{"role":"assistant","content":null},
			{"role":"assistant"},
			{"type":"message","role":"assistant","content":[]},
			{"role":"assistant","content":[
				{"type":"output_text","text":"   "},
				{"type":"input_text","text":""},
				{"type":"text","text":null},
				null,
				"  "
			]},
			{"role":"assistant","content":"keep string"},
			{"role":"assistant","content":[{"type":"output_text","text":"keep block"}]},
			{"role":"assistant","content":[{"type":"refusal","refusal":"keep refusal"}]}
		]
	}`)

	normalized, audit, err := NormalizeRequestByID(RequestNormalizerOpenAIResponsesCompatible, body)
	require.NoError(t, err)
	require.NoError(t, ValidateRequestByID(RequestNormalizerOpenAIResponsesCompatible, normalized))
	assert.Equal(t, types.ProtocolNormalizationAudit{
		Normalizer:                    RequestNormalizerOpenAIResponsesCompatible,
		EmptyAssistantMessagesDropped: 6,
	}, audit)

	var request struct {
		Input []map[string]json.RawMessage `json:"input"`
	}
	require.NoError(t, common.Unmarshal(normalized, &request))
	require.Len(t, request.Input, 4)
	assert.Equal(t, "keep user", responseTestString(t, request.Input[0]["content"]))
	assert.Equal(t, "keep string", responseTestString(t, request.Input[1]["content"]))
	assert.Contains(t, string(request.Input[2]["content"]), "keep block")
	assert.Contains(t, string(request.Input[3]["content"]), "keep refusal")
}

func TestValidateOpenAIResponsesCompatibleRejectsStructurallyEmptyAssistantItems(t *testing.T) {
	tests := []struct {
		name string
		item string
	}{
		{name: "empty string", item: `{"role":"assistant","content":""}`},
		{name: "whitespace string", item: `{"role":"assistant","content":"  "}`},
		{name: "null content", item: `{"role":"assistant","content":null}`},
		{name: "missing content", item: `{"role":"assistant"}`},
		{name: "empty array", item: `{"role":"assistant","content":[]}`},
		{name: "blank blocks", item: `{"role":"assistant","content":[{"type":"output_text","text":" "}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRequestByID(
				RequestNormalizerOpenAIResponsesCompatible,
				[]byte(`{"input":[`+test.item+`]}`),
			)
			require.ErrorContains(t, err, "empty responses assistant item")
		})
	}
}

func TestValidateOpenAIResponsesCompatibleRejectsInvalidCallIDs(t *testing.T) {
	require.ErrorContains(t, ValidateRequestByID(
		RequestNormalizerOpenAIResponsesCompatible,
		[]byte(`{"input":[{"type":"function_call","call_id":"call:bad"}]}`),
	), "does not match")

	_, _, err := NormalizeRequestByID(
		RequestNormalizerOpenAIResponsesCompatible,
		[]byte(`{"input":[{"type":"function_call","call_id":123}]}`),
	)
	require.ErrorContains(t, err, "call_id must be a string")
}

// responseTestString decodes one string field from a normalized Responses fixture.
func responseTestString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	require.NoError(t, common.Unmarshal(raw, &value))
	return value
}
