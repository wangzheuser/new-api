package model

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupConsumeLogTestDB creates the minimal primary and log database fixture.
func setupConsumeLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousLogDB := LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	dsn := fmt.Sprintf("file:consume-log-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	require.NoError(t, db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, setting TEXT)").Error)
	DB = db
	LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestRecordConsumeLogPersistsFullBillingDataButPrintsFixedSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupConsumeLogTestDB(t)
	previousLogConsume := common.LogConsumeEnabled
	previousDataExport := common.DataExportEnabled
	previousRedis := common.RedisEnabled
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
	common.RedisEnabled = false
	t.Cleanup(func() {
		common.LogConsumeEnabled = previousLogConsume
		common.DataExportEnabled = previousDataExport
		common.RedisEnabled = previousRedis
	})

	var output bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	previousErrorWriter := gin.DefaultErrorWriter
	gin.DefaultWriter = &output
	gin.DefaultErrorWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		gin.DefaultErrorWriter = previousErrorWriter
		common.LogWriterMu.Unlock()
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "fixture-user")
	ctx.Set(common.RequestIdKey, "REQUEST_ID")
	ctx.Set(common.UpstreamRequestIdKey, "UPSTREAM_REQUEST_ID")
	normalizedInputTokens := 81
	params := RecordConsumeLogParams{
		ChannelId:           12,
		PromptTokens:        100,
		CompletionTokens:    30,
		InputTokens:         &normalizedInputTokens,
		CacheCreationTokens: 5,
		CacheReadTokens:     14,
		ModelName:           "MODEL_X",
		TokenName:           "TOKEN_NAME",
		Quota:               4321,
		Content:             "settled",
		TokenId:             9,
		UseTimeSeconds:      6,
		IsStream:            true,
		Group:               "vip",
		Other: map[string]interface{}{
			"protocol":           "responses",
			"route":              "native",
			"subscription_id":    77,
			"subscription_quota": 321,
			"audit_marker":       "FULL_OTHER_MARKER",
		},
	}

	RecordConsumeLog(ctx, 42, params)

	var stored Log
	require.NoError(t, db.First(&stored).Error)
	assert.Equal(t, 42, stored.UserId)
	assert.Equal(t, 12, stored.ChannelId)
	assert.Equal(t, "MODEL_X", stored.ModelName)
	assert.Equal(t, 100, stored.PromptTokens)
	assert.Equal(t, 30, stored.CompletionTokens)
	assert.Equal(t, normalizedInputTokens, stored.InputTokens)
	assert.Equal(t, 5, stored.CacheCreationTokens)
	assert.Equal(t, 14, stored.CacheReadTokens)
	assert.Equal(t, 4321, stored.Quota)
	assert.Equal(t, 9, stored.TokenId)
	assert.Equal(t, "REQUEST_ID", stored.RequestId)
	assert.Equal(t, "UPSTREAM_REQUEST_ID", stored.UpstreamRequestId)
	assert.True(t, stored.IsStream)

	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(stored.Other, &other))
	assert.Equal(t, "responses", other["protocol"])
	assert.Equal(t, "native", other["route"])
	assert.EqualValues(t, 77, other["subscription_id"])
	assert.EqualValues(t, 321, other["subscription_quota"])
	assert.Equal(t, "FULL_OTHER_MARKER", other["audit_marker"])

	line := output.String()
	assert.Contains(t, line, "record consume log: user_id=42 channel_id=12 model=MODEL_X quota=4321 stream=true duration_seconds=6")
	assert.NotContains(t, line, "params=")
	assert.NotContains(t, line, "FULL_OTHER_MARKER")
	assert.NotContains(t, line, `"other"`)

	common.LogConsumeEnabled = false
	RecordConsumeLog(ctx, 43, params)
	var count int64
	require.NoError(t, db.Model(&Log{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	assert.Equal(t, 1, strings.Count(output.String(), "record consume log:"))
}
