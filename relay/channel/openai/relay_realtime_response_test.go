package openai

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
)

func TestRewriteRealtimeClientMessage(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RequestedModelName: "CLIENT_ALIAS",
		AttemptModelName:   "UPSTREAM_MODEL",
		ChannelMeta:        &relaycommon.ChannelMeta{UpstreamModelName: "UPSTREAM_MODEL"},
	}
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "session created",
			input:    `{"type":"session.created","session":{"model":"UPSTREAM_MODEL"}}`,
			expected: `{"type":"session.created","session":{"model":"CLIENT_ALIAS"}}`,
		},
		{
			name:     "response done",
			input:    `{"type":"response.done","response":{"model":"UPSTREAM_MODEL","output":[{"content":"UPSTREAM_MODEL"}]}}`,
			expected: `{"type":"response.done","response":{"model":"CLIENT_ALIAS","output":[{"content":"UPSTREAM_MODEL"}]}}`,
		},
		{
			name:     "error",
			input:    `{"type":"error","error":{"message":"UPSTREAM_MODEL unavailable"}}`,
			expected: `{"type":"error","error":{"message":"CLIENT_ALIAS unavailable"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := rewriteRealtimeClientMessage([]byte(tt.input), info)
			assert.JSONEq(t, tt.expected, string(actual))
		})
	}
}
