package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// TestFormatUserLogsStripsQuotaSaturation verifies the admin-only quota
// saturation marker (nested under other.admin_info) is removed for non-admin
// log views, since formatUserLogs strips the whole admin_info object.
func TestFormatUserLogsStripsQuotaSaturation(t *testing.T) {
	other := common.MapToJsonStr(map[string]interface{}{
		"model_price": 0.004,
		"admin_info": map[string]interface{}{
			"quota_saturation": map[string]interface{}{
				"op":      "QuotaFromDecimal",
				"kind":    "overflow",
				"clamped": common.MaxQuota,
			},
		},
	})
	logs := []*Log{{Other: other}}

	formatUserLogs(logs, 0)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	_, hasAdminInfo := parsed["admin_info"]
	require.False(t, hasAdminInfo, "admin_info (and nested quota_saturation) must be stripped for non-admin views")
	// Non-admin billing fields remain visible.
	require.Contains(t, parsed, "model_price")
}

// TestFormatUserLogsStripsOperationalFields verifies ordinary log views retain
// billing details while removing routing, override and upstream identifiers.
func TestFormatUserLogsStripsOperationalFields(t *testing.T) {
	other := common.MapToJsonStr(map[string]interface{}{
		"model_price":                  0.004,
		"task_id":                      "task-public",
		"reason":                       "upstream HOST failed on channel 17",
		"channel_id":                   17,
		"channel_name":                 "supplier-a",
		"channel_type":                 8,
		"is_model_mapped":              true,
		"upstream_model_name":          "supplier-model",
		"request_conversion":           "openai->gemini",
		"po":                           []string{"replace instructions with operator prompt"},
		"is_system_prompt_overwritten": true,
		"audit_info":                   map[string]interface{}{"route": "/api/internal"},
		"stream_status":                "broken",
	})
	logs := []*Log{{
		ChannelId:         17,
		ChannelName:       "supplier-a",
		UpstreamRequestId: "upstream-request-id",
		Other:             other,
	}}

	formatUserLogs(logs, 0)

	require.Zero(t, logs[0].ChannelId)
	require.Empty(t, logs[0].ChannelName)
	require.Empty(t, logs[0].UpstreamRequestId)
	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	require.Equal(t, 0.004, parsed["model_price"])
	require.Equal(t, "task-public", parsed["task_id"])
	require.Equal(t, "任务失败，额度已退回", parsed["reason"])
	for _, key := range []string{
		"channel_id",
		"channel_name",
		"channel_type",
		"is_model_mapped",
		"upstream_model_name",
		"request_conversion",
		"po",
		"is_system_prompt_overwritten",
		"audit_info",
		"stream_status",
	} {
		require.NotContains(t, parsed, key)
	}
}
