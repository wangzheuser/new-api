package model

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestLogQueriesLatestPerRequest verifies deduplication happens before result filters and pagination.
func TestLogQueriesLatestPerRequest(t *testing.T) {
	truncateTables(t)
	logs := []*Log{
		{UserId: 1, CreatedAt: 100, Type: LogTypeError, Content: "retry", TokenId: 7, RequestId: "req-a", IsIntermediate: true},
		{UserId: 1, CreatedAt: 101, Type: LogTypeConsume, Content: "success", TokenId: 7, RequestId: "req-a"},
		{UserId: 1, CreatedAt: 102, Type: LogTypeError, Content: "older same second", TokenId: 7, RequestId: "req-b", IsIntermediate: true},
		{UserId: 1, CreatedAt: 102, Type: LogTypeError, Content: "final same second", TokenId: 7, RequestId: "req-b"},
		{UserId: 1, CreatedAt: 103, Type: LogTypeManage, Content: "empty one"},
		{UserId: 1, CreatedAt: 104, Type: LogTypeManage, Content: "empty two"},
		{UserId: 2, CreatedAt: 105, Type: LogTypeConsume, Content: "other user", TokenId: 8, RequestId: "req-c"},
		{UserId: 1, CreatedAt: 106, Type: LogTypeError, Content: "in-flight retry", TokenId: 7, RequestId: "req-in-flight", IsIntermediate: true},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	allLogs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", "", "", false)
	require.NoError(t, err)
	assert.Len(t, allLogs, 8)
	assert.EqualValues(t, 8, total)

	latestLogs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", "", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 5, total)
	assert.ElementsMatch(t, []string{"success", "final same second", "empty one", "empty two", "other user"}, logContents(latestLogs))

	errorLogs, total, err := GetAllLogs(LogTypeError, 0, 0, "", "", "", 0, 20, 0, "", "", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, errorLogs, 1)
	assert.Equal(t, "final same second", errorLogs[0].Content)

	userLogs, total, err := GetUserLogs(1, LogTypeUnknown, 0, 0, "", "", 0, 20, "", "", true)
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

	inFlight, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", "req-in-flight", "", true)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, inFlight)

	tokenLogs, err := GetLogByTokenId(7)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"success", "final same second"}, logContents(tokenLogs))
}

// TestLogQueriesLatestPerRequestAcrossTimeRange verifies an intermediate log is not treated as final at a time-range boundary.
func TestLogQueriesLatestPerRequestAcrossTimeRange(t *testing.T) {
	truncateTables(t)
	logs := []*Log{
		{UserId: 1, CreatedAt: 100, Type: LogTypeError, Content: "retry before boundary", RequestId: "req-cross-boundary", IsIntermediate: true},
		{UserId: 1, CreatedAt: 101, Type: LogTypeManage, Content: "legacy empty request"},
		{UserId: 1, CreatedAt: 200, Type: LogTypeConsume, Content: "success after boundary", RequestId: "req-cross-boundary"},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)

	allLogs, total, err := GetAllLogs(LogTypeUnknown, 0, 150, "", "", "", 0, 20, 0, "", "", "", false)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.ElementsMatch(t, []string{"retry before boundary", "legacy empty request"}, logContents(allLogs))

	latestLogs, total, err := GetAllLogs(LogTypeUnknown, 0, 150, "", "", "", 0, 20, 0, "", "", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, latestLogs, 1)
	assert.Equal(t, "legacy empty request", latestLogs[0].Content)

	userLogs, total, err := GetUserLogs(1, LogTypeUnknown, 0, 150, "", "", 0, 20, "", "", true)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, userLogs, 1)
	assert.Equal(t, "legacy empty request", userLogs[0].Content)
}

func TestLogQueriesRetryOnlyByUseChannelChain(t *testing.T) {
	for _, dialect := range []common.DatabaseType{
		common.DatabaseTypeSQLite, common.DatabaseTypeMySQL,
		common.DatabaseTypePostgreSQL, common.DatabaseTypeClickHouse,
	} {
		t.Run(string(dialect), func(t *testing.T) {
			var db *gorm.DB
			var err error
			if dialect == common.DatabaseTypeSQLite {
				db, err = gorm.Open(sqlite.Open(":memory:"), newGormConfig(false))
			} else {
				env := "RETRY_LOG_" + strings.ToUpper(string(dialect)) + "_DSN"
				dsn := os.Getenv(env)
				if dsn == "" {
					t.Skipf("set %s to an empty disposable database", env)
				}
				t.Setenv("RETRY_LOG_TEST_DSN", dsn)
				var dbType common.DatabaseType
				db, dbType, err = chooseDB("RETRY_LOG_TEST_DSN", true)
				require.Equal(t, dialect, dbType)
			}
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.False(t, db.Migrator().HasTable(&Log{}), "use an empty disposable database")
			previousDB, previousType := LOG_DB, common.LogDatabaseType()
			LOG_DB = db
			common.SetLogDatabaseType(dialect)
			initCol()
			t.Cleanup(func() {
				LOG_DB = previousDB
				common.SetLogDatabaseType(previousType)
				initCol()
				assert.NoError(t, db.Migrator().DropTable(&Log{}))
			})
			if dialect == common.DatabaseTypeClickHouse {
				require.NoError(t, db.Exec(clickHouseLogCreateTableSQL(0)).Error)
			} else {
				require.NoError(t, db.AutoMigrate(&Log{}))
			}
			versionQuery := "SELECT version()"
			if dialect == common.DatabaseTypeSQLite {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			now := time.Now().Unix()
			logs := []*Log{
				{CreatedAt: now - 8, Type: LogTypeConsume, Content: "single with metadata", RequestId: "single",
					Other: `{"admin_info":{"use_channel":["118"]},"billing_source":"wallet","cache_tokens":0,"request_conversion":["OpenAI Compatible","Claude Messages"]}`, Quota: 10},
				{CreatedAt: now - 7, Type: LogTypeError, Content: "older retry", RequestId: "final-single",
					Other: `{"admin_info":{"use_channel":["118","120"]}}`},
				{CreatedAt: now - 6, Type: LogTypeError, Content: "intermediate retry", RequestId: "final-single", IsIntermediate: true,
					Other: `{"admin_info":{"use_channel":["118","120"]}}`},
				{CreatedAt: now - 5, Type: LogTypeConsume, Content: "final single", RequestId: "final-single",
					Other: `{"admin_info":{"use_channel":["118"]}}`, Quota: 20},
				{CreatedAt: now - 4, Type: LogTypeConsume, Content: "different channel retry", RequestId: "different",
					Other: `{"admin_info":{"use_channel":["118","120"]},"billing_source":"wallet"}`, Quota: 30, PromptTokens: 10, CompletionTokens: 20},
				{CreatedAt: now - 3, Type: LogTypeConsume, Content: "same channel retry", RequestId: "same",
					Other: `{"admin_info":{"use_channel":["118","118"]}}`, Quota: 40, PromptTokens: 20, CompletionTokens: 30},
				{CreatedAt: now - 2, Type: LogTypeConsume, Content: "legacy log", RequestId: "legacy", Quota: 50},
				{CreatedAt: now - 1, Type: LogTypeConsume, Content: "empty chain", RequestId: "empty",
					Other: `{"admin_info":{"use_channel":[]},"request_conversion":["OpenAI Compatible","Claude Messages"]}`, Quota: 60},
				{CreatedAt: now, Type: LogTypeConsume, Content: "missing chain", RequestId: "missing",
					Other: `{"admin_info":{},"request_conversion":["OpenAI Compatible","Claude Messages"]}`, Quota: 70},
			}
			require.NoError(t, db.Create(&logs).Error)

			retryLogs, total, err := GetAllLogs(LogTypeUnknown, now-10, now+1, "", "", "", 0, 20, 0, "", "", "", true, true)
			require.NoError(t, err)
			assert.EqualValues(t, 2, total)
			assert.Equal(t, []string{"same channel retry", "different channel retry"}, logContents(retryLogs))

			page, total, err := GetAllLogs(LogTypeUnknown, now-10, now+1, "", "", "", 1, 1, 0, "", "", "", true, true)
			require.NoError(t, err)
			assert.EqualValues(t, 2, total)
			assert.Equal(t, []string{"different channel retry"}, logContents(page))

			stat, err := SumUsedQuota(LogTypeUnknown, now-10, now+1, "", "", "", 0, "", "", "", true, true)
			require.NoError(t, err)
			assert.Equal(t, 70, stat.Quota)
			assert.EqualValues(t, 2, stat.RequestCount)
			assert.Equal(t, 2, stat.Rpm)
			assert.Equal(t, 80, stat.Tpm)

			filtered, total, err := GetAllLogs(LogTypeUnknown, now-10, now+1, "", "", "", 0, 20, 0, "", "same", "", true, true)
			require.NoError(t, err)
			assert.EqualValues(t, 1, total)
			assert.Equal(t, []string{"same channel retry"}, logContents(filtered))
			stat, err = SumUsedQuota(LogTypeUnknown, now-10, now+1, "", "", "", 0, "", "same", "", true, true)
			require.NoError(t, err)
			assert.Equal(t, 40, stat.Quota)
			assert.EqualValues(t, 1, stat.RequestCount)
			assert.Equal(t, 1, stat.Rpm)
			assert.Equal(t, 50, stat.Tpm)
		})
	}
}

// TestLogSearchCountIsCapped verifies list endpoints never scan beyond the advertised result ceiling.
func TestLogSearchCountIsCapped(t *testing.T) {
	truncateTables(t)
	logs := make([]Log, logSearchCountLimit+1)
	for i := range logs {
		logs[i] = Log{
			UserId:    1,
			CreatedAt: int64(i + 1),
			Type:      LogTypeConsume,
			RequestId: "request-count-limit",
		}
	}
	require.NoError(t, LOG_DB.CreateInBatches(&logs, 500).Error)

	allLogs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 1, 0, "", "", "", false)
	require.NoError(t, err)
	assert.EqualValues(t, logSearchCountLimit, total)
	assert.Len(t, allLogs, 1)

	userLogs, total, err := GetUserLogs(1, LogTypeUnknown, 0, 0, "", "", 0, 1, "", "", false)
	require.NoError(t, err)
	assert.EqualValues(t, logSearchCountLimit, total)
	assert.Len(t, userLogs, 1)
}

// logContents returns log content values for order-independent assertions.
func logContents(logs []*Log) []string {
	contents := make([]string, 0, len(logs))
	for _, log := range logs {
		contents = append(contents, log.Content)
	}
	return contents
}
