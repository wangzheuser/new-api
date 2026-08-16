package common

import (
	"testing"

	basecommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassifyResponseSemantics covers provider-native terminal shapes that must survive conversion.
func TestClassifyResponseSemantics(t *testing.T) {
	tests := []struct {
		name      string
		format    types.RelayFormat
		body      string
		outcome   string
		rejection string
		output    string
		truncated bool
		usage     string
	}{
		{
			name:      "OpenAI content filter",
			format:    types.RelayFormatOpenAI,
			body:      `{"choices":[{"finish_reason":"content_filter","message":{"role":"assistant","content":""}}],"usage":{"prompt_tokens":9,"completion_tokens":0}}`,
			outcome:   ResponseOutcomeRejected,
			rejection: ResponseRejectionAll,
			output:    ResponseOutputEmpty,
			usage:     ResponseUsageUpstream,
		},
		{
			name:      "OpenAI mixed rejection keeps deliverable text",
			format:    types.RelayFormatOpenAI,
			body:      `{"choices":[{"finish_reason":"content_filter","message":{"content":""}},{"finish_reason":"stop","message":{"content":"answer"}}]}`,
			outcome:   ResponseOutcomeCompleted,
			rejection: ResponseRejectionPartial,
			output:    ResponseOutputText,
			usage:     ResponseUsageAbsent,
		},
		{
			name:      "Claude max tokens with tool remains tool call",
			format:    types.RelayFormatClaude,
			body:      `{"stop_reason":"max_tokens","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{}}],"usage":{"input_tokens":10,"output_tokens":2}}`,
			outcome:   ResponseOutcomeToolCall,
			rejection: ResponseRejectionNone,
			output:    ResponseOutputToolCalls,
			truncated: true,
			usage:     ResponseUsageUpstream,
		},
		{
			name:      "Gemini prompt block without candidates",
			format:    types.RelayFormatGemini,
			body:      `{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":8}}`,
			outcome:   ResponseOutcomeRejected,
			rejection: ResponseRejectionAll,
			output:    ResponseOutputEmpty,
			usage:     ResponseUsageUpstream,
		},
		{
			name:      "Responses incomplete output",
			format:    types.RelayFormatOpenAIResponses,
			body:      `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}],"usage":{"input_tokens":4,"output_tokens":6}}`,
			outcome:   ResponseOutcomeIncomplete,
			rejection: ResponseRejectionNone,
			output:    ResponseOutputText,
			truncated: true,
			usage:     ResponseUsageUpstream,
		},
		{
			name:      "Responses incomplete status with unknown reason",
			format:    types.RelayFormatOpenAIResponses,
			body:      `{"status":"incomplete","incomplete_details":{"reason":"provider_future_reason"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`,
			outcome:   ResponseOutcomeIncomplete,
			rejection: ResponseRejectionNone,
			output:    ResponseOutputText,
			usage:     ResponseUsageAbsent,
		},
		{
			name:      "Responses incomplete status without output",
			format:    types.RelayFormatOpenAIResponses,
			body:      `{"status":"incomplete","output":[]}`,
			outcome:   ResponseOutcomeIncomplete,
			rejection: ResponseRejectionNone,
			output:    ResponseOutputEmpty,
			usage:     ResponseUsageAbsent,
		},
		{
			name:      "Responses refusal normalizes completed status as rejection",
			format:    types.RelayFormatOpenAIResponses,
			body:      `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"blocked"}]}]}`,
			outcome:   ResponseOutcomeRejected,
			rejection: ResponseRejectionAll,
			output:    ResponseOutputEmpty,
			usage:     ResponseUsageAbsent,
		},
		{
			name:      "Responses reasoning metadata does not make refusal partial",
			format:    types.RelayFormatOpenAIResponses,
			body:      `{"status":"completed","output":[{"type":"reasoning","summary":[]},{"type":"message","content":[{"type":"refusal","refusal":"blocked"}]}]}`,
			outcome:   ResponseOutcomeRejected,
			rejection: ResponseRejectionAll,
			output:    ResponseOutputReasoningOnly,
			usage:     ResponseUsageAbsent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			semantics := ClassifyResponseSemantics(test.format, []byte(test.body))
			assert.Equal(t, test.outcome, semantics.Response.PrimaryOutcome)
			assert.Equal(t, test.rejection, semantics.Response.RejectionState)
			assert.Equal(t, test.output, semantics.Response.OutputState)
			assert.Equal(t, test.truncated, semantics.Response.Truncated)
			assert.Equal(t, test.usage, semantics.Response.UsageState)
			require.NotEmpty(t, semantics.Response.Items)
			if test.name == "Responses refusal normalizes completed status as rejection" {
				assert.Equal(t, "completed", semantics.Response.ProviderReason)
				assert.Equal(t, "content_filter", semantics.Response.NormalizedReason)
			}
		})
	}
}

// TestResponseSemanticsItemsAlwaysSerialize keeps semantic condition paths stable for empty responses.
func TestResponseSemanticsItemsAlwaysSerialize(t *testing.T) {
	semantics := ClassifyResponseSemantics(types.RelayFormatOpenAI, []byte(`{"choices":[]}`))
	data, err := basecommon.Marshal(semantics)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"items":[]`)
}

// TestClassifyResponseSemanticsKeepsUnknownOutputsUnknown verifies that internal
// reasoning and unrecognized terminal reasons are not treated as deliverable text.
func TestClassifyResponseSemanticsKeepsUnknownOutputsUnknown(t *testing.T) {
	tests := []struct {
		name             string
		body             string
		output           string
		hasReasoning     bool
		providerReason   string
		normalizedReason string
		hasItem          bool
	}{
		{
			name:             "reasoning only with unknown provider reason",
			body:             `{"choices":[{"finish_reason":"provider_future_reason","message":{"content":"","reasoning_content":"internal result"}}]}`,
			output:           ResponseOutputReasoningOnly,
			hasReasoning:     true,
			providerReason:   "provider_future_reason",
			normalizedReason: "provider_future_reason",
			hasItem:          true,
		},
		{
			name:             "empty choice with unknown provider reason",
			body:             `{"choices":[{"finish_reason":"provider_future_reason","message":{"content":""}}]}`,
			output:           ResponseOutputEmpty,
			providerReason:   "provider_future_reason",
			normalizedReason: "provider_future_reason",
			hasItem:          true,
		},
		{
			name:   "response without choices",
			body:   `{"choices":[]}`,
			output: ResponseOutputEmpty,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			semantics := ClassifyResponseSemantics(types.RelayFormatOpenAI, []byte(test.body))
			assert.Equal(t, ResponseOutcomeUnknown, semantics.Response.PrimaryOutcome)
			assert.Equal(t, test.output, semantics.Response.OutputState)
			assert.Equal(t, test.hasReasoning, semantics.Response.HasReasoning)
			assert.Equal(t, test.providerReason, semantics.Response.ProviderReason)
			assert.Equal(t, test.normalizedReason, semantics.Response.NormalizedReason)
			if !test.hasItem {
				assert.Empty(t, semantics.Response.Items)
				return
			}
			require.Len(t, semantics.Response.Items, 1)
			assert.Equal(t, ResponseOutcomeUnknown, semantics.Response.Items[0].PrimaryOutcome)
			assert.Equal(t, test.hasReasoning, semantics.Response.Items[0].HasReasoning)
			assert.False(t, semantics.Response.Items[0].HasText)
		})
	}
}

// TestMergeResponseSemanticsPreservesProviderFacts verifies conversion cannot erase a native rejection.
func TestMergeResponseSemanticsPreservesProviderFacts(t *testing.T) {
	provider := ClassifyResponseSemantics(types.RelayFormatGemini, []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`))
	tests := []struct {
		name       string
		body       string
		itemFailed bool
	}{
		{
			name: "converted empty response",
			body: `{"choices":[{"finish_reason":"stop","message":{"content":""}}]}`,
		},
		{
			name:       "converted embedded error",
			body:       `{"error":{"message":"request rejected","type":"provider_error"}}`,
			itemFailed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := ClassifyResponseSemantics(types.RelayFormatOpenAI, []byte(test.body))
			client.Client = ResponseEndpointSemantics{Format: types.RelayFormatOpenAI, HTTPStatus: 200}

			merged := MergeResponseSemantics(provider, client)
			require.Equal(t, types.RelayFormat(types.RelayFormatGemini), merged.Upstream.Format)
			require.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), merged.Client.Format)
			require.Len(t, merged.Response.Items, 1)
			assert.Equal(t, ResponseRejectionAll, merged.Response.RejectionState)
			assert.Equal(t, ResponseOutcomeRejected, merged.Response.PrimaryOutcome)
			assert.Equal(t, "SAFETY", merged.Response.ProviderReason)
			assert.Equal(t, "content_filter", merged.Response.NormalizedReason)
			assert.Equal(t, ResponseRejectionAll, merged.Response.Items[0].RejectionState)
			assert.Equal(t, ResponseOutcomeRejected, merged.Response.Items[0].PrimaryOutcome)
			assert.Equal(t, test.itemFailed, merged.Response.Items[0].Failed)
		})
	}
}

// TestMergeResponseSemanticsPreservesMixedProviderChoices verifies conversion
// does not collapse a real multi-choice partial rejection into an all rejection.
func TestMergeResponseSemanticsPreservesMixedProviderChoices(t *testing.T) {
	provider := ClassifyResponseSemantics(types.RelayFormatOpenAI, []byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}},{"finish_reason":"stop","message":{"content":"answer"}}]}`))
	client := ClassifyResponseSemantics(types.RelayFormatOpenAI, []byte(`{"choices":[{"finish_reason":"stop","message":{"content":"answer"}}]}`))

	merged := MergeResponseSemantics(provider, client)
	require.Len(t, merged.Response.Items, 2)
	assert.Equal(t, ResponseRejectionPartial, merged.Response.RejectionState)
	assert.Equal(t, ResponseOutcomeCompleted, merged.Response.PrimaryOutcome)
}
