package contexttruncate

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestOutputOverrideCompatibility keeps only bounded scalar writes compatible with trimming.
func TestOutputOverrideCompatibility(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		conflict bool
	}{
		{`{"operations":[{"mode":"set","path":"max_tokens","value":65536,"conditions":[{"path":"max_tokens","mode":"gt","value":65536}],"logic":"AND"}]}`, false},
		{`{"max_completion_tokens":65536}`, false},
		{`{"operations":[{"mode":"set","path":"max_output_tokens","value":64}]}`, false},
		{`{"operations":[{"mode":"set","path":"generationConfig.maxOutputTokens","value":65536}]}`, false},
		{`{"generationConfig":{"maxOutputTokens":64}}`, true},
		{`{"max_tokens":0}`, true},
		{`{"max_tokens":-1}`, true},
		{`{"max_tokens":1.5}`, true},
		{`{"max_tokens":"64"}`, true},
		{`{"max_tokens":18446744073709551615}`, true},
		{`{"operations":[{"mode":"delete","path":"max_tokens"}]}`, true},
		{`{"operations":[{"mode":"copy","from":"max_tokens","to":"messages"}]}`, true},
		{`{"operations":[{"mode":"set","path":"generationConfig.*","value":64}]}`, true},
		{`{"max_tokens":64,"messages":[]}`, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var overrides map[string]interface{}
			require.NoError(t, common.UnmarshalJsonStr(tc.raw, &overrides))
			assert.Equal(t, tc.conflict, Conflicts(overrides))
		})
	}
}
