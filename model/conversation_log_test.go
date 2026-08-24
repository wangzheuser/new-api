package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateConversationLogsPersistsWholeBatch verifies grouped writes keep every captured attempt.
func TestCreateConversationLogsPersistsWholeBatch(t *testing.T) {
	truncateTables(t)
	logs := []*ConversationLog{
		{CreatedAt: 1, RequestId: "batch-1", StorageBytes: 10},
		{CreatedAt: 2, RequestId: "batch-2", StorageBytes: 20},
		{CreatedAt: 3, RequestId: "batch-3", StorageBytes: 30},
	}
	require.NoError(t, CreateConversationLogs(logs))

	var persisted int64
	require.NoError(t, LOG_DB.Model(&ConversationLog{}).Where("request_id IN ?", []string{"batch-1", "batch-2", "batch-3"}).Count(&persisted).Error)
	assert.EqualValues(t, 3, persisted)
}

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

// TestGetConversationLogsCapsTotal verifies metadata pages bound count work and omit captured bodies.
func TestGetConversationLogsCapsTotal(t *testing.T) {
	truncateTables(t)
	logs := make([]ConversationLog, logSearchCountLimit+1)
	for i := range logs {
		logs[i] = ConversationLog{
			CreatedAt:         int64(i + 1),
			RequestId:         "conversation-count-limit",
			ClientRequestBody: "captured body",
			StorageBytes:      13,
		}
	}
	require.NoError(t, LOG_DB.CreateInBatches(&logs, 500).Error)

	page, total, err := GetConversationLogs(ConversationLogQuery{}, 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, logSearchCountLimit, total)
	require.Len(t, page, 1)
	assert.Empty(t, page[0].ClientRequestBody)
}

// TestGetConversationLogSummaryAggregatesInOneResult verifies count and logical storage stay consistent.
func TestGetConversationLogSummaryAggregatesInOneResult(t *testing.T) {
	truncateTables(t)
	logs := []ConversationLog{
		{CreatedAt: 1, StorageBytes: 11},
		{CreatedAt: 2, StorageBytes: 29},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	summary, err := GetConversationLogSummary()
	require.NoError(t, err)
	assert.EqualValues(t, 2, summary.RecordCount)
	assert.EqualValues(t, 40, summary.StorageBytes)
}

// TestDeleteConversationLogsUsesFiltersAcrossBatches verifies bounded deletion keeps non-matching records.
func TestDeleteConversationLogsUsesFiltersAcrossBatches(t *testing.T) {
	truncateTables(t)
	logs := []ConversationLog{
		{CreatedAt: 1, RequestId: "delete-1", UserId: 7, ClientRequestBody: "first"},
		{CreatedAt: 2, RequestId: "keep-user", UserId: 8, ClientRequestBody: "second"},
		{CreatedAt: 3, RequestId: "delete-2", UserId: 7, ClientRequestBody: "third"},
		{CreatedAt: 4, RequestId: "keep-time", UserId: 7, ClientRequestBody: "fourth"},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	deleted, err := DeleteConversationLogs(context.Background(), ConversationLogQuery{EndTime: 3, UserId: 7}, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)

	var remaining []ConversationLog
	require.NoError(t, LOG_DB.Order("created_at asc").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	assert.Equal(t, "keep-user", remaining[0].RequestId)
	assert.Equal(t, "keep-time", remaining[1].RequestId)
}
