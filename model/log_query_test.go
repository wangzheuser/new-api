package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogQueriesLatestPerRequest verifies deduplication happens before result filters and pagination.
func TestLogQueriesLatestPerRequest(t *testing.T) {
	truncateTables(t)
	logs := []*Log{
		{UserId: 1, CreatedAt: 100, Type: LogTypeError, Content: "retry", RequestId: "req-a"},
		{UserId: 1, CreatedAt: 101, Type: LogTypeConsume, Content: "success", RequestId: "req-a"},
		{UserId: 1, CreatedAt: 102, Type: LogTypeError, Content: "older same second", RequestId: "req-b"},
		{UserId: 1, CreatedAt: 102, Type: LogTypeError, Content: "final same second", RequestId: "req-b"},
		{UserId: 1, CreatedAt: 103, Type: LogTypeManage, Content: "empty one"},
		{UserId: 1, CreatedAt: 104, Type: LogTypeManage, Content: "empty two"},
		{UserId: 2, CreatedAt: 105, Type: LogTypeConsume, Content: "other user", RequestId: "req-c"},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	allLogs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", "", "", false)
	require.NoError(t, err)
	assert.Len(t, allLogs, 7)
	assert.EqualValues(t, 7, total)

	latestLogs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", "", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.ElementsMatch(t, []string{"success", "final same second", "empty one", "empty two", "other user"}, logContents(latestLogs))

	errorLogs, total, err := GetAllLogs(LogTypeError, 0, 0, "", "", "", 0, 20, 0, "", "", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, errorLogs, 1)
	assert.Equal(t, "final same second", errorLogs[0].Content)

	userLogs, total, err := GetUserLogs(1, LogTypeUnknown, 0, 0, "", "", 0, 20, "", "", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 4, total)
	assert.ElementsMatch(t, []string{"success", "final same second", "empty one", "empty two"}, logContents(userLogs))

	page, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 1, 2, 0, "", "", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.Len(t, page, 2)

	exact, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", "req-a", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, exact, 1)
	assert.Equal(t, "success", exact[0].Content)
}

// logContents returns log content values for order-independent assertions.
func logContents(logs []*Log) []string {
	contents := make([]string, 0, len(logs))
	for _, log := range logs {
		contents = append(contents, log.Content)
	}
	return contents
}
