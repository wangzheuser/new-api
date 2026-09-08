package contexttruncate

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"testing"
)

// TestTrimWholeTurnsAcrossProtocols ensures old call/result pairs leave together and prompts survive.
func TestTrimWholeTurnsAcrossProtocols(t *testing.T) {
	cases := []struct {
		name, field, body string
		before, after     int
	}{
		{"chat", "messages", `{"tools":[{"type":"function"}],"messages":[{"role":"system","content":"rules"},{"role":"user","content":"old"},{"role":"assistant","tool_calls":[{"id":"a"}]},{"role":"tool","tool_call_id":"a","content":"result"},{"role":"user","content":"latest"}]}`, 5, 2},
		{"claude", "messages", `{"system":"rules","messages":[{"role":"user","content":"old"},{"role":"assistant","content":[{"type":"tool_use","id":"a"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"result"}]},{"role":"user","content":"latest"}]}`, 4, 1},
		{"responses", "input", `{"instructions":"rules","input":[{"role":"user","content":"old"},{"type":"function_call","call_id":"a"},{"type":"function_call_output","call_id":"a","output":"result"},{"role":"user","content":"latest"}]}`, 4, 1},
		{"gemini", "contents", `{"systemInstruction":{"parts":[{"text":"rules"}]},"contents":[{"role":"user","parts":[{"text":"old"}]},{"role":"model","parts":[{"functionCall":{"name":"lookup"}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup"}}]},{"role":"user","parts":[{"text":"latest"}]}]}`, 4, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			count := func(b []byte) (int, error) { return len(gjson.GetBytes(b, tc.field).Array()) * 100, nil }
			got, result, err := Trim(context.Background(), body, tc.after*100, 1, count)
			require.NoError(t, err)
			assert.True(t, result.Applied)
			assert.Equal(t, tc.before*100, result.Before)
			assert.Equal(t, tc.after*100, result.After)
			assert.Equal(t, 1, result.RemovedTurns)
			assert.Contains(t, string(got), "latest")
			assert.NotContains(t, string(got), "\"old\"")
			for _, key := range []string{"system", "instructions", "systemInstruction", "tools"} {
				assert.JSONEq(t, objectOrNull(gjson.Get(tc.body, key)), objectOrNull(gjson.GetBytes(got, key)))
			}
			assert.Equal(t, tc.body, string(body))
		})
	}
}

// objectOrNull provides a comparable JSON value for retained optional request fields.
func objectOrNull(value gjson.Result) string {
	if !value.Exists() {
		return "null"
	}
	return value.Raw
}

// TestTrimProtectedAndInvalidHistories covers local rejection rather than silent context damage.
func TestTrimProtectedAndInvalidHistories(t *testing.T) {
	count := func(b []byte) (int, error) { return len(gjson.GetBytes(b, "messages").Array()) * 100, nil }
	for _, tc := range []struct{ body, err string }{
		{`{"messages":[{"role":"user","content":"only"}]}`, "context_length_exceeded"},
		{`{"messages":[{"role":"user","content":"old"},{"role":"assistant","tool_calls":[{"id":"pending"}]},{"role":"user","content":"new"}]}`, "context_length_exceeded"},
		{`{"messages":[{"role":"tool","tool_call_id":"missing"},{"role":"user","content":"new"}]}`, "invalid_tool_reference"},
	} {
		_, _, err := Trim(context.Background(), []byte(tc.body), 100, 1, func(b []byte) (int, error) { n, e := count(b); return n + 1, e })
		require.ErrorContains(t, err, tc.err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Trim(ctx, []byte(`{"messages":[{"role":"user"},{"role":"user"}]}`), 100, 1, count)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestShapeAndOverrides excludes unsupported media and operation-style content mutations.
func TestShapeAndOverrides(t *testing.T) {
	for _, tc := range []struct{ body, reason string }{
		{`{"input":"hello","previous_response_id":"r"}`, "provider_state_reference"},
		{`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"url"}]}],"previous_response_id":"r"}`, "multimodal"},
		{`{"input":[{"type":"reasoning","encrypted_content":"opaque"}]}`, "opaque_history"},
		{`{"messages":[{"role":"assistant","function_call":{"name":"old"}}]}`, "legacy_tool_history"},
	} {
		_, reason := Shape([]byte(tc.body))
		assert.Equal(t, tc.reason, reason)
	}
	for _, path := range []string{"messages.0.content", "/input/0", "contents[0]", "tools"} {
		assert.True(t, Conflicts(map[string]interface{}{"operations": []interface{}{map[string]interface{}{"path": path}}}))
	}
	assert.False(t, Conflicts(map[string]interface{}{"temperature": 0}))
	assert.True(t, Conflicts(map[string]interface{}{"operations": []interface{}{map[string]interface{}{"mode": "move", "from": "temporary", "to": "messages"}}}))
	assert.False(t, Conflicts(map[string]interface{}{"operations": []interface{}{map[string]interface{}{"phase": "response", "mode": "return_error", "conditions": []interface{}{map[string]interface{}{"path": "model", "value": "m"}}}}}))
	assert.False(t, Conflicts(map[string]interface{}{"operations": []interface{}{map[string]interface{}{"mode": "set", "path": "temperature", "value": 0, "conditions": []interface{}{map[string]interface{}{"path": "model", "value": "m"}}}}}))
}
