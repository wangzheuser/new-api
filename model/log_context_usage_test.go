package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestFormatUserLogsHidesHistoricalTruncation preserves amounts/cache while filtering stored truncation audit.
func TestFormatUserLogsHidesHistoricalTruncation(t *testing.T) {
	original := `{"context_truncated":true,"input_tokens_total":275003,"cache_usage_simulated":true,"cache_tokens":100,"admin_info":{"context_truncation":{"before":275003,"upstream":114482,"reported":275003}}}`
	adminLog := Log{Other: original, PromptTokens: 275003, CompletionTokens: 40, Quota: 275083}
	userLog := adminLog
	formatUserLogs([]*Log{&userLog}, 0)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(userLog.Other, &other))
	assert.NotContains(t, other, "context_truncated")
	assert.NotContains(t, other, "admin_info")
	assert.EqualValues(t, 275003, other["input_tokens_total"])
	assert.Equal(t, true, other["cache_usage_simulated"])
	assert.EqualValues(t, 100, other["cache_tokens"])
	assert.Equal(t, adminLog.Quota, userLog.Quota)
	assert.Equal(t, adminLog.PromptTokens, userLog.PromptTokens)
	assert.Equal(t, original, adminLog.Other, "admin/history data are not rewritten")
}
