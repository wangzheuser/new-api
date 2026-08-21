package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeleteOldLogBatchDeletesOnlyBoundedExpiredRows verifies relational cleanup stays within its batch boundary.
func TestDeleteOldLogBatchDeletesOnlyBoundedExpiredRows(t *testing.T) {
	truncateTables(t)
	logs := []*Log{
		{CreatedAt: 10, Type: LogTypeConsume},
		{CreatedAt: 20, Type: LogTypeError},
		{CreatedAt: 30, Type: LogTypeManage},
		{CreatedAt: 40, Type: LogTypeLogin},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	deleted, err := DeleteOldLogBatch(context.Background(), 30, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	var remaining []*Log
	require.NoError(t, LOG_DB.Order("created_at asc, id asc").Find(&remaining).Error)
	require.Len(t, remaining, 3)
	assert.EqualValues(t, 20, remaining[0].CreatedAt)
	assert.EqualValues(t, 30, remaining[1].CreatedAt)
	assert.EqualValues(t, 40, remaining[2].CreatedAt)

	deleted, err = DeleteOldLogBatch(context.Background(), 30, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	deleted, err = DeleteOldLogBatch(context.Background(), 30, 10)
	require.NoError(t, err)
	assert.Zero(t, deleted)

	remaining = nil
	require.NoError(t, LOG_DB.Order("created_at asc, id asc").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	assert.EqualValues(t, 30, remaining[0].CreatedAt)
	assert.EqualValues(t, 40, remaining[1].CreatedAt)
}
