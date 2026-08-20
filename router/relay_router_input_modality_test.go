package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type relayInputModalityTestFixture struct {
	router       *gin.Engine
	credential   string
	requestModel string
}

// setupRelayInputModalityRouter 使用隔离的认证和渠道数据构建真实中继路由链。
func setupRelayInputModalityRouter(t *testing.T) relayInputModalityTestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	previousMemoryCacheEnabled := common.MemoryCacheEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousSQLitePath := common.SQLitePath
	previousMasterNode := common.IsMasterNode
	previousPerformanceConfig := common.GetPerformanceMonitorConfig()
	previousRateLimitEnabled := setting.ModelRequestRateLimitEnabled
	previousGroups := setting.UserUsableGroups2JSONString()

	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.IsMasterNode = false
	common.SetPerformanceMonitorConfig(common.PerformanceMonitorConfig{Enabled: false})
	setting.ModelRequestRateLimitEnabled = false
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":""}`))

	dsn := fmt.Sprintf("file:relay-router-input-modality-%d?mode=memory&cache=shared", time.Now().UnixNano())
	common.SQLitePath = dsn
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitDB())
	db := model.DB
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.UserSubscription{},
		&model.UserGroupGrant{},
		&model.Channel{},
	))
	model.DB = db

	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		common.SQLitePath = previousSQLitePath
		common.IsMasterNode = previousMasterNode
		common.SetPerformanceMonitorConfig(previousPerformanceConfig)
		setting.ModelRequestRateLimitEnabled = previousRateLimitEnabled
		_ = setting.UpdateUserUsableGroupsByJSONString(previousGroups)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	user := &model.User{
		Username: "relay-modality-admin",
		Password: "fixture-password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    1_000_000,
		AffCode:  "relay-modality-admin-aff",
	}
	require.NoError(t, db.Create(user).Error)

	const tokenKey = "modalityfixture"
	token := &model.Token{
		UserId:         user.Id,
		Key:            tokenKey,
		Status:         common.TokenStatusEnabled,
		Name:           "relay modality fixture",
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, db.Create(token).Error)

	channel := &model.Channel{
		Type:   constant.ChannelTypeOpenAI,
		Key:    "fixture-upstream-key",
		Status: common.ChannelStatusEnabled,
		Name:   "relay modality fixture",
		Models: "source",
		Group:  "default",
	}
	channel.SetSetting(dto.ChannelSettings{
		ModelInputModalities: types.ModelInputModalities{
			"source": {types.InputModalityText},
		},
	})
	require.NoError(t, db.Create(channel).Error)

	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)

	return relayInputModalityTestFixture{
		router:       engine,
		credential:   fmt.Sprintf("sk-%s-%d", tokenKey, channel.Id),
		requestModel: "source",
	}
}

// TestRelayRouterReturnsProtocolNativeInputModalityErrors 验证完整 HTTP 中间件和路由链返回协议原生错误。
func TestRelayRouterReturnsProtocolNativeInputModalityErrors(t *testing.T) {
	fixture := setupRelayInputModalityRouter(t)
	tests := []struct {
		name           string
		path           string
		body           string
		setCredential  func(*http.Request, string)
		assertEnvelope func(*testing.T, []byte) string
	}{
		{
			name: "chat completions control",
			path: "/v1/chat/completions",
			body: `{"model":"source","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]}]}`,
			setCredential: func(request *http.Request, credential string) {
				request.Header.Set("Authorization", "Bearer "+credential)
			},
			assertEnvelope: assertOpenAIInputModalityError,
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: `{"model":"source","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/a.png"}]}]}`,
			setCredential: func(request *http.Request, credential string) {
				request.Header.Set("Authorization", "Bearer "+credential)
			},
			assertEnvelope: assertOpenAIInputModalityError,
		},
		{
			name: "responses compaction",
			path: "/v1/responses/compact",
			body: `{"model":"source","input":[{"type":"input_image","image_url":"https://example.test/a.png"}]}`,
			setCredential: func(request *http.Request, credential string) {
				request.Header.Set("Authorization", "Bearer "+credential)
			},
			assertEnvelope: assertOpenAIInputModalityError,
		},
		{
			name: "anthropic messages",
			path: "/v1/messages",
			body: `{"model":"source","max_tokens":16,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`,
			setCredential: func(request *http.Request, credential string) {
				request.Header.Set("x-api-key", credential)
				request.Header.Set("anthropic-version", "2023-06-01")
			},
			assertEnvelope: assertClaudeInputModalityError,
		},
		{
			name: "anthropic count tokens",
			path: "/v1/messages/count_tokens",
			body: `{"model":"source","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`,
			setCredential: func(request *http.Request, credential string) {
				request.Header.Set("x-api-key", credential)
				request.Header.Set("anthropic-version", "2023-06-01")
			},
			assertEnvelope: assertClaudeInputModalityError,
		},
		{
			name: "gemini generate content",
			path: "/v1beta/models/source:generateContent",
			body: `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`,
			setCredential: func(request *http.Request, credential string) {
				request.Header.Set("x-goog-api-key", credential)
			},
			assertEnvelope: assertGeminiInputModalityError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			test.setCredential(request, fixture.credential)

			fixture.router.ServeHTTP(recorder, request)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
			requestID := recorder.Header().Get(common.RequestIdKey)
			require.NotEmpty(t, requestID)
			message := test.assertEnvelope(t, recorder.Body.Bytes())
			assert.Contains(t, message, "model "+fixture.requestModel+" has no declared image input capability")
			assert.Contains(t, message, requestID)
		})
	}
}

// assertOpenAIInputModalityError 校验 OpenAI 风格错误对象。
func assertOpenAIInputModalityError(t *testing.T, body []byte) string {
	t.Helper()
	var response struct {
		Error types.OpenAIError `json:"error"`
	}
	require.NoError(t, common.Unmarshal(body, &response))
	assert.Equal(t, string(types.ErrorCodeUnsupportedInputModality), response.Error.Type)
	assert.Equal(t, string(types.ErrorCodeUnsupportedInputModality), response.Error.Code)
	assert.Empty(t, response.Error.Param)
	return response.Error.Message
}

// assertClaudeInputModalityError 校验 Anthropic Messages 风格错误对象。
func assertClaudeInputModalityError(t *testing.T, body []byte) string {
	t.Helper()
	var response struct {
		Type  string            `json:"type"`
		Error types.ClaudeError `json:"error"`
	}
	require.NoError(t, common.Unmarshal(body, &response))
	assert.Equal(t, "error", response.Type)
	assert.Equal(t, string(types.ErrorCodeUnsupportedInputModality), response.Error.Type)
	return response.Error.Message
}

// assertGeminiInputModalityError 校验 Google RPC 风格错误对象。
func assertGeminiInputModalityError(t *testing.T, body []byte) string {
	t.Helper()
	var response dto.GeminiErrorResponse
	require.NoError(t, common.Unmarshal(body, &response))
	assert.Equal(t, http.StatusBadRequest, response.Error.Code)
	assert.Equal(t, "INVALID_ARGUMENT", response.Error.Status)
	return response.Error.Message
}
