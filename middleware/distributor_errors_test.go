package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRecordDistributorSetupErrorWritesFinalLog(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousErrorLog := common.RedisEnabled, constant.ErrorLogEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled, constant.ErrorLogEnabled = false, true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.RedisEnabled, constant.ErrorLogEnabled = previousRedis, previousErrorLog
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, db.Create(&model.User{Id: 7, Username: "distributor-test", Group: "default"}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("id", 7)
	c.Set("username", "distributor-test")
	c.Set("original_model", "test-model")
	c.Set("group", "default")
	c.Set("token_name", "test-token")
	c.Set("token_id", 11)
	c.Set(common.RequestIdKey, "distributor-error-request")

	recordDistributorSetupError(c, &model.Channel{Id: 85, Type: 1, Name: "disabled-keys"}, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey))

	var entry model.Log
	require.NoError(t, db.Where("request_id = ?", "distributor-error-request").First(&entry).Error)
	assert.Equal(t, model.LogTypeError, entry.Type)
	assert.False(t, entry.IsIntermediate)
	assert.Equal(t, 503, mustLogStatusCode(t, entry.Other))
}

func TestDistributeSkipsChannelWithNoEnabledKeys(t *testing.T) {
	previousDB, previousMemoryCache, previousRetryTimes := model.DB, common.MemoryCacheEnabled, common.RetryTimes
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	dsn := fmt.Sprintf("file:distributor-fallback-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	common.RetryTimes = 1
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCache
		common.RetryTimes = previousRetryTimes
		common.SetDatabaseTypes(previousMainType, previousLogType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	highPriority := int64(10)
	lowPriority := int64(1)
	blocked := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Key: "DISABLED", Status: common.ChannelStatusEnabled,
		Name: "blocked", Models: "MODEL_X", Group: "default", Priority: &highPriority,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled}},
	}
	available := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Key: "AVAILABLE", Status: common.ChannelStatusEnabled,
		Name: "available", Models: "MODEL_X", Group: "default", Priority: &lowPriority,
	}
	require.NoError(t, db.Create(blocked).Error)
	require.NoError(t, db.Create(available).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "MODEL_X", ChannelId: blocked.Id, Enabled: true, Priority: &highPriority, Weight: 100}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "MODEL_X", ChannelId: available.Id, Enabled: true, Priority: &lowPriority, Weight: 100}).Error)

	var selectedID int
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Set("id", 7)
		c.Set("username", "distributor-test")
		c.Set("original_model", "MODEL_X")
	}, Distribute(), func(c *gin.Context) {
		selectedID = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"MODEL_X"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, available.Id, selectedID)
}

// mustLogStatusCode reads the normalized status from a serialized log payload.
func mustLogStatusCode(t *testing.T, raw string) int {
	t.Helper()
	other, err := common.StrToMap(raw)
	require.NoError(t, err)
	return int(other["status_code"].(float64))
}
