package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupModelRateLimitTest configures deterministic in-memory rate limiting.
func setupModelRateLimitTest(t *testing.T, groupLimits string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousTotal := setting.ModelRequestRateLimitCount
	previousSuccess := setting.ModelRequestRateLimitSuccessCount
	previousGroups := setting.ModelRequestRateLimitGroup2JSONString()
	previousResponseConfig := setting.UserModelRateLimitResponseConfig2JSONString()
	previousRedisEnabled := common.RedisEnabled
	previousRDB := common.RDB
	previousErrorLogEnabled := constant.ErrorLogEnabled
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousDBType := common.MainDatabaseType()
	previousLogDBType := common.LogDatabaseType()

	// Each fixture owns an isolated rules table because the middleware now checks user overrides.
	dsn := fmt.Sprintf("file:model-rate-limit-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserModelRateLimit{}, &model.Log{}))
	model.DB = db
	model.LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)

	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = 0
	setting.ModelRequestRateLimitSuccessCount = 100
	require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(groupLimits))
	require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(`{"delay_seconds":0,"default_response":{"status_code":429,"error_message":"default rate limit"},"group_responses":{}}`))
	common.RedisEnabled = false
	constant.ErrorLogEnabled = false
	inMemoryRateLimiter = &common.InMemoryRateLimiter{}

	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		setting.ModelRequestRateLimitCount = previousTotal
		setting.ModelRequestRateLimitSuccessCount = previousSuccess
		require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(previousGroups))
		require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(previousResponseConfig))
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRDB
		constant.ErrorLogEnabled = previousErrorLogEnabled
		inMemoryRateLimiter = &common.InMemoryRateLimiter{}
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.SetMainDatabaseType(previousDBType)
		common.SetLogDatabaseType(previousLogDBType)
		sqlDB, err := db.DB()
		if err == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	router := gin.New()
	router.GET("/:user/:group", func(c *gin.Context) {
		var userId int
		_, err := fmt.Sscanf(c.Param("user"), "%d", &userId)
		require.NoError(t, err)
		c.Set("id", userId)
		c.Set(common.RequestIdKey, fmt.Sprintf("request-%d", userId))
		c.Set("username", fmt.Sprintf("user-%d", userId))
		common.SetContextKey(c, constant.ContextKeyUsingGroup, c.Param("group"))
		common.SetContextKey(c, constant.ContextKeyTokenGroup, "raw-token-group")
		c.Next()
	}, ModelRequestRateLimit(), func(c *gin.Context) {
		if c.Query("fail") == "1" {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})
	return router
}

// TestModelRequestRateLimitSeparatesUserGroups verifies buckets are user-and-group scoped.
func TestModelRequestRateLimitSeparatesUserGroups(t *testing.T) {
	router := setupModelRateLimitTest(t, `{"group-a":[1,10],"group-b":[1,10],"raw-token-group":[0,1]}`)

	request := func(path string) int {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Code
	}

	assert.Equal(t, http.StatusOK, request("/101/group-a"))
	assert.Equal(t, http.StatusTooManyRequests, request("/101/group-a"))
	assert.Equal(t, http.StatusOK, request("/101/group-b"))
	assert.Equal(t, http.StatusOK, request("/202/group-a"))
}

// TestMemoryModelRequestRateLimitCountsOnlySuccessfulResponses verifies failures do not consume success capacity.
func TestMemoryModelRequestRateLimitCountsOnlySuccessfulResponses(t *testing.T) {
	router := setupModelRateLimitTest(t, `{"success-only":[0,1]}`)

	request := func(path string) int {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Code
	}

	assert.Equal(t, http.StatusInternalServerError, request("/303/success-only?fail=1"))
	assert.Equal(t, http.StatusOK, request("/303/success-only"))
	assert.Equal(t, http.StatusTooManyRequests, request("/303/success-only"))
}

// TestModelRequestRateLimitKeyIncludesGroup locks the Redis and memory key contract.
func TestModelRequestRateLimitKeyIncludesGroup(t *testing.T) {
	assert.Equal(t, "rateLimit:MRRL:101:group-a", modelRequestRateLimitKey(ModelRequestRateLimitCountMark, "101:group-a"))
	assert.Equal(t, "rateLimit:MRRLS:101:group-a", modelRequestRateLimitKey(ModelRequestRateLimitSuccessCountMark, "101:group-a"))
}

// TestUserModelRateLimitReturnsConfiguredResponse verifies the independent rule response contract.
func TestUserModelRateLimitReturnsConfiguredResponse(t *testing.T) {
	router := setupModelRateLimitTest(t, `{}`)
	require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(`{"delay_seconds":0,"default_response":{"status_code":451,"error_message":"custom rejection"},"group_responses":{}}`))
	require.NoError(t, model.CreateUserModelRateLimit(&model.UserModelRateLimit{
		UserId:       404,
		GroupName:    "vip",
		TotalCount:   1,
		SuccessCount: 10,
	}))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/404/vip", nil))
	assert.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/404/vip", nil))
	assert.Equal(t, 451, second.Code)
	assert.JSONEq(t, `{"error":{"message":"custom rejection (request id: request-404)","type":"new_api_error","code":"model_rate_limit_exceeded"}}`, second.Body.String())
	assert.True(t, second.Flushed)
}

// TestUserModelRateLimitSuccessCountUsesCustomResponse verifies successful-request rejections share the response policy.
func TestUserModelRateLimitSuccessCountUsesCustomResponse(t *testing.T) {
	router := setupModelRateLimitTest(t, `{}`)
	require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(`{"delay_seconds":0,"default_response":{"status_code":409,"error_message":"success limit"},"group_responses":{}}`))
	require.NoError(t, model.CreateUserModelRateLimit(&model.UserModelRateLimit{
		UserId:       405,
		GroupName:    "vip",
		TotalCount:   0,
		SuccessCount: 1,
	}))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/405/vip", nil))
	assert.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/405/vip", nil))
	assert.Equal(t, http.StatusConflict, second.Code)
	assert.Contains(t, second.Body.String(), "success limit (request id: request-405)")
}

// TestUserModelRateLimitCanceledDelayWritesNoResponse verifies cancellation exits the delayed rejection path.
func TestUserModelRateLimitCanceledDelayWritesNoResponse(t *testing.T) {
	router := setupModelRateLimitTest(t, `{}`)
	require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(`{"delay_seconds":60,"default_response":{"status_code":429,"error_message":"delayed"},"group_responses":{}}`))
	require.NoError(t, model.CreateUserModelRateLimit(&model.UserModelRateLimit{
		UserId:       406,
		GroupName:    "vip",
		TotalCount:   1,
		SuccessCount: 10,
	}))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/406/vip", nil))
	assert.Equal(t, http.StatusOK, first.Code)

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	second := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/406/vip", nil).WithContext(requestContext)
	router.ServeHTTP(second, request)
	assert.Empty(t, second.Body.String())
	assert.False(t, second.Flushed)
}

// TestUserModelRateLimitResponseAndLogStayConsistent verifies the public response and usage log share final data.
func TestUserModelRateLimitResponseAndLogStayConsistent(t *testing.T) {
	router := setupModelRateLimitTest(t, `{}`)
	constant.ErrorLogEnabled = true
	require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(`{"delay_seconds":0,"default_response":{"status_code":403,"error_message":"logged rejection"},"group_responses":{}}`))
	require.NoError(t, model.DB.Create(&model.User{
		Id:       407,
		Username: "user-407",
		Password: "password",
		Group:    "vip",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, model.CreateUserModelRateLimit(&model.UserModelRateLimit{
		UserId:       407,
		GroupName:    "vip",
		TotalCount:   1,
		SuccessCount: 10,
	}))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/407/vip", nil))
	assert.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/407/vip", nil))
	assert.Equal(t, http.StatusForbidden, second.Code)

	var storedLog model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ?", "request-407").First(&storedLog).Error)
	expectedMessage := "logged rejection (request id: request-407)"
	assert.Contains(t, second.Body.String(), expectedMessage)
	assert.Equal(t, "status_code=403, "+expectedMessage, storedLog.Content)
	assert.Equal(t, "request-407", storedLog.RequestId)
	assert.Equal(t, "vip", storedLog.Group)
	assert.Equal(t, model.LogTypeError, storedLog.Type)

	var other map[string]interface{}
	require.NoError(t, common.Unmarshal([]byte(storedLog.Other), &other))
	assert.EqualValues(t, 403, other["status_code"])
	assert.Equal(t, true, other["public_error"])
	assert.Equal(t, "user_group", other["rate_limit_scope"])
	assert.Equal(t, "total", other["rate_limit_kind"])
	assert.Equal(t, "global", other["rate_limit_response_source"])
	assert.Equal(t, true, other["response_written"])
}

// TestRedisUserModelRateLimitReturnsConfiguredResponse covers the shared Redis counter path.
func TestRedisUserModelRateLimitReturnsConfiguredResponse(t *testing.T) {
	router := setupModelRateLimitTest(t, `{}`)
	redisServer, err := miniredis.Run()
	require.NoError(t, err)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() {
		require.NoError(t, redisClient.Close())
		redisServer.Close()
	})
	common.RDB = redisClient
	common.RedisEnabled = true

	require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(`{"delay_seconds":0,"default_response":{"status_code":418,"error_message":"redis rejection"},"group_responses":{}}`))
	require.NoError(t, model.CreateUserModelRateLimit(&model.UserModelRateLimit{
		UserId:       408,
		GroupName:    "vip",
		TotalCount:   1,
		SuccessCount: 1,
	}))

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/408/vip", nil))
	assert.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/408/vip", nil))
	assert.Equal(t, http.StatusTeapot, second.Code)
	assert.Contains(t, second.Body.String(), "redis rejection (request id: request-408)")
}
