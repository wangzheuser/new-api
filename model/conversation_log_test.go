package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTrimConversationLogsDeletesOnlyRequiredOldestRows verifies the storage cap does not discard a whole batch unnecessarily.
func TestTrimConversationLogsDeletesOnlyRequiredOldestRows(t *testing.T) {
	truncateTables(t)
	logs := []*ConversationLog{
		{CreatedAt: 1, StorageBytes: 10},
		{CreatedAt: 2, StorageBytes: 10},
		{CreatedAt: 3, StorageBytes: 10},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	deleted, err := TrimConversationLogs(context.Background(), 20, 200)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	var remaining []*ConversationLog
	require.NoError(t, LOG_DB.Order("created_at asc").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	assert.EqualValues(t, 2, remaining[0].CreatedAt)
	assert.EqualValues(t, 3, remaining[1].CreatedAt)
}
