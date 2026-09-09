package dto

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestContextSimplificationIgnoresRemovedFields checks the persisted configuration contract.
func TestContextSimplificationIgnoresRemovedFields(t *testing.T) {
	for _, raw := range []string{
		`{"mode":"custom","window_tokens":1000,"output_reserve_tokens":100,"threshold_percent":1,"keep_recent_turns":200,"safety_tokens":999}`,
		`{"mode":"custom","window_tokens":1000,"output_reserve_tokens":100,"threshold_percent":"ignored","keep_recent_turns":{},"safety_tokens":false}`,
	} {
		var rule ContextTruncationRule
		require.NoError(t, common.UnmarshalJsonStr(raw, &rule))
		require.NoError(t, rule.Validate(false))
		budget, err := rule.Budget(0)
		require.NoError(t, err)
		assert.Equal(t, 880, budget)
		assert.Equal(t, 1, rule.Keep())
		serialized, err := common.Marshal(rule)
		require.NoError(t, err)
		for _, field := range []string{"threshold_percent", "keep_recent_turns", "safety_tokens"} {
			assert.NotContains(t, string(serialized), field)
		}
	}
}
