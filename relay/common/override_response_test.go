package common

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func responseRule(value map[string]interface{}, conditions ...interface{}) map[string]interface{} {
	return map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"id":          "policy_rejection",
				"description": "Map provider rejection to a client error.",
				"phase":       "response",
				"mode":        "return_error",
				"value":       value,
				"conditions":  conditions,
			},
		},
	}
}

// TestApplyResponseOverrideSources protects body, semantic, context, and legacy source behavior.
func TestApplyResponseOverrideSources(t *testing.T) {
	body := []byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}],"usage":{"prompt_tokens":9}}`)
	input := BufferedRelayResponse{
		Body:                body,
		UpstreamStatusCode:  http.StatusOK,
		CandidateStatusCode: http.StatusOK,
		CandidateFormat:     types.RelayFormatOpenAI,
		Headers:             http.Header{"Content-Type": {"application/json"}},
	}
	tests := []struct {
		name      string
		condition map[string]interface{}
	}{
		{name: "body", condition: map[string]interface{}{"source": "body", "path": "choices.0.finish_reason", "mode": "full", "value": "content_filter"}},
		{name: "semantic", condition: map[string]interface{}{"source": "semantic", "path": "response.primary_outcome", "mode": "full", "value": ResponseOutcomeRejected}},
		{name: "context", condition: map[string]interface{}{"source": "context", "path": "response.upstream_http_status", "mode": "full", "value": http.StatusOK}},
		{name: "legacy body fallback", condition: map[string]interface{}{"path": "choices.0.finish_reason", "mode": "full", "value": "content_filter"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			override := responseRule(map[string]interface{}{"message": "模型拒绝执行该指令", "code": "content_filter"}, test.condition)
			result, err := ApplyResponseOverride(override, nil, input)
			require.NoError(t, err)
			assert.Equal(t, ResponseOverrideReplaceError, result.Disposition)
			require.NotNil(t, result.Error)
			assert.Equal(t, http.StatusForbidden, result.Error.StatusCode)
			assert.True(t, result.Error.SkipRetry)
			assert.Equal(t, "policy_rejection", result.RuleID)
			assert.Equal(t, 1, result.RuleIndex)
			assert.Equal(t, "Map provider rejection to a client error.", result.Description)
		})
	}
}

// TestValidateParamOverrideRejectsInvalidResponseRules covers persistence-time contracts.
func TestValidateParamOverrideRejectsInvalidResponseRules(t *testing.T) {
	tests := []struct {
		name     string
		override map[string]interface{}
		contains string
	}{
		{name: "unknown request mode", override: map[string]interface{}{"operations": []interface{}{map[string]interface{}{"mode": "script"}}}, contains: "unsupported mode"},
		{name: "response mutation", override: map[string]interface{}{"operations": []interface{}{map[string]interface{}{"phase": "response", "mode": "set", "path": "x", "value": 1}}}, contains: "only supports return_error"},
		{name: "response status below error range", override: responseRule(map[string]interface{}{"message": "x", "status_code": 302}), contains: "status code out of range"},
		{name: "explicit response retry", override: responseRule(map[string]interface{}{"message": "x", "skip_retry": false}), contains: "skip_retry must be true"},
		{name: "unknown source", override: responseRule(map[string]interface{}{"message": "x"}, map[string]interface{}{"source": "provider", "path": "x", "mode": "full", "value": 1}), contains: "unsupported condition source"},
		{name: "unknown comparison", override: responseRule(map[string]interface{}{"message": "x"}, map[string]interface{}{"source": "body", "path": "x", "mode": "regex", "value": 1}), contains: "unsupported comparison mode"},
		{name: "semantic source on request", override: map[string]interface{}{"operations": []interface{}{map[string]interface{}{"phase": "request", "mode": "set", "path": "x", "value": 1, "conditions": []interface{}{map[string]interface{}{"source": "semantic", "path": "response.primary_outcome", "mode": "full", "value": "rejected"}}}}}, contains: "only available in response phase"},
		{name: "rule after response catch-all", override: map[string]interface{}{"operations": []interface{}{map[string]interface{}{"phase": "response", "mode": "return_error", "value": map[string]interface{}{"message": "fallback"}}, map[string]interface{}{"phase": "response", "mode": "return_error", "value": map[string]interface{}{"message": "specific"}, "conditions": []interface{}{map[string]interface{}{"source": "body", "path": "x", "mode": "full", "value": 1}}}}}, contains: "must be last"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateParamOverride(test.override)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.contains)
		})
	}
}

// TestApplyResponseOverrideReturnsPassOnConfigError keeps malformed runtime
// configuration separate from the response disposition.
func TestApplyResponseOverrideReturnsPassOnConfigError(t *testing.T) {
	override := responseRule(map[string]interface{}{"message": "bad", "skip_retry": false})
	info := &RelayInfo{}
	result, err := ApplyResponseOverride(override, info, BufferedRelayResponse{
		Body:                []byte(`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`),
		UpstreamStatusCode:  http.StatusOK,
		CandidateStatusCode: http.StatusOK,
		UpstreamFormat:      types.RelayFormatOpenAI,
		CandidateFormat:     types.RelayFormatOpenAI,
	})
	require.Error(t, err)
	assert.Equal(t, ResponseOverridePass, result.Disposition)
	assert.Nil(t, result.Error)
	assert.Equal(t, ResponseOutcomeRejected, info.ResponseSemantics.Response.PrimaryOutcome)
	assert.Equal(t, ResponseRejectionAll, info.ResponseSemantics.Response.RejectionState)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), info.ResponseSemantics.Upstream.Format)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAI), info.ResponseSemantics.Client.Format)
	assert.Equal(t, http.StatusOK, info.ResponseSemantics.Upstream.HTTPStatus)
	assert.Equal(t, http.StatusOK, info.ResponseSemantics.Client.HTTPStatus)
}

// TestApplyResponseOverrideUsesAbsoluteRuleIndex keeps audit indices aligned
// with the complete operations array, including rules from other phases.
func TestApplyResponseOverrideUsesAbsoluteRuleIndex(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "request",
				"mode":  "set",
				"path":  "metadata.request_rule",
				"value": true,
			},
			map[string]interface{}{
				"description": "Second operation in the document.",
				"phase":       "response",
				"mode":        "return_error",
				"value":       map[string]interface{}{"message": "blocked"},
			},
		},
	}

	result, err := ApplyResponseOverride(override, nil, BufferedRelayResponse{
		Body:                []byte(`{"choices":[]}`),
		UpstreamStatusCode:  http.StatusOK,
		CandidateStatusCode: http.StatusOK,
		UpstreamFormat:      types.RelayFormatOpenAI,
		CandidateFormat:     types.RelayFormatOpenAI,
	})

	require.NoError(t, err)
	assert.Equal(t, ResponseOverrideReplaceError, result.Disposition)
	assert.Equal(t, 2, result.RuleIndex)
	assert.Equal(t, "response:2", result.RuleID)
	assert.Equal(t, "Second operation in the document.", result.Description)
}

// TestHasResponseOverrideOnlyInspectsNormalizedPhase keeps buffering detection side-effect free.
func TestHasResponseOverrideOnlyInspectsNormalizedPhase(t *testing.T) {
	assert.True(t, HasResponseOverride(map[string]interface{}{"operations": []interface{}{map[string]interface{}{"phase": " RESPONSE ", "mode": "return_error"}}}))
	assert.True(t, HasResponseOverride(map[string]interface{}{"operations": []interface{}{map[string]interface{}{"phase": "response", "conditions": "invalid"}}}))
	assert.False(t, HasResponseOverride(map[string]interface{}{"operations": []interface{}{map[string]interface{}{"phase": "request", "mode": "set"}}}))
}

// TestParamOverrideCanonicalizesOperationSyntax verifies validation and runtime
// dispatch use the same normalized mode, logic, and condition mode values.
func TestParamOverrideCanonicalizesOperationSyntax(t *testing.T) {
	override := map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": " RESPONSE ",
				"mode":  " RETURN_ERROR ",
				"logic": " AND ",
				"value": map[string]interface{}{"message": "blocked"},
				"conditions": []interface{}{
					map[string]interface{}{"source": " BODY ", "path": " allowed ", "mode": " FULL ", "value": true},
					map[string]interface{}{"source": " BODY ", "path": " blocked ", "mode": " FULL ", "value": true},
				},
			},
		},
	}
	require.NoError(t, ValidateParamOverride(override))

	result, err := ApplyResponseOverride(override, nil, BufferedRelayResponse{
		Body:                []byte(`{"allowed":true,"blocked":false}`),
		UpstreamStatusCode:  http.StatusOK,
		CandidateStatusCode: http.StatusOK,
		UpstreamFormat:      types.RelayFormatOpenAI,
		CandidateFormat:     types.RelayFormatOpenAI,
	})

	require.NoError(t, err)
	assert.Equal(t, ResponseOverridePass, result.Disposition)
}
