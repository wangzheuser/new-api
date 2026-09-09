package dto

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestCachePolicyContracts covers independent draws, explicit zero and invalid saved values.
func TestCachePolicyContracts(t *testing.T) {
	p := CacheUsageSimulationPolicy{}
	assert.Equal(t, [4]int{20, 60, 30, 50}, p.Percentages())
	for _, tc := range []struct {
		sample       [2]int
		create, read int
	}{
		{[2]int{20, 60}, 0, 0}, {[2]int{19, 60}, 300, 0}, {[2]int{20, 59}, 0, 500}, {[2]int{19, 59}, 300, 500},
	} {
		c, r := SplitCacheUsage(1001, p, tc.sample)
		assert.Equal(t, tc.create, c)
		assert.Equal(t, tc.read, r)
	}
	for _, raw := range []string{
		`{"creation_trigger_percent":0,"read_trigger_percent":0}`,
		`{"creation_trigger_percent":100,"read_trigger_percent":100}`,
		`{"creation_token_percent":51,"read_token_percent":50}`,
		`{"read_trigger_percent":-1}`,
	} {
		var policy CacheUsageSimulationPolicy
		require.NoError(t, common.UnmarshalJsonStr(raw, &policy))
		if raw == `{"creation_trigger_percent":0,"read_trigger_percent":0}` {
			require.NoError(t, policy.Validate(false))
			c, r := SplitCacheUsage(1000, policy, [2]int{})
			assert.Zero(t, c+r)
		} else {
			assert.Error(t, policy.Validate(false))
		}
	}
	var invalid CacheUsageSimulationPolicy
	assert.Error(t, common.UnmarshalJsonStr(`{"read_trigger_percent":1.5}`, &invalid))
	c, r := SplitCacheUsage(int(^uint(0)>>1), p, [2]int{})
	assert.Zero(t, c+r)
}

// TestContextRuleBudget covers required reserves and request-dependent budgets.
func TestContextRuleBudget(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"mode":"custom","window_tokens":1000}`, false},
		{`{"mode":"custom","window_tokens":1000,"output_reserve_tokens":0}`, false},
		{`{"mode":"custom","window_tokens":1000,"output_reserve_tokens":980}`, false},
		{`{"mode":"custom","window_tokens":1000,"output_reserve_tokens":100}`, true},
		{`{"mode":"off"}`, true},
	} {
		var rule ContextTruncationRule
		require.NoError(t, common.UnmarshalJsonStr(tc.raw, &rule))
		if !tc.valid {
			assert.Error(t, rule.Validate(false))
			continue
		}
		require.NoError(t, rule.Validate(false))
		if rule.Mode == "off" {
			continue
		}
		for _, test := range []struct{ output, budget int }{{0, 880}, {100, 880}, {200, 780}} {
			budget, err := rule.Budget(test.output)
			require.NoError(t, err)
			assert.Equal(t, test.budget, budget)
		}
	}
}
