package router

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type relayInputModalityTestFixture struct {
	router        *gin.Engine
	credential    string
	requestModel  string
	unpinnedToken string
	textChannel   *model.Channel
}

// setupRelayInputModalityRouter 使用隔离的认证和渠道数据构建真实中继路由链。
func setupRelayInputModalityRouter(t *testing.T, testDSN string) relayInputModalityTestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB, previousLogDB := model.DB, model.LOG_DB
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
	t.Setenv("SQL_DSN", testDSN)
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitDB())
	require.NoError(t, model.InitLogDB())
	db := model.DB
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.UserSubscription{},
		&model.UserGroupGrant{},
		&model.Channel{},
		&model.Ability{},
		&model.Log{},
	))
	model.DB = db

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
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
	user.SetSetting(dto.UserSetting{AcceptUnsetRatioModel: true})
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
		Models: "source,source-openai-compact",
		Group:  "default",
	}
	channel.SetSetting(dto.ChannelSettings{
		ModelInputModalities: types.ModelInputModalities{
			"source":                {types.InputModalityText},
			"source-openai-compact": {types.InputModalityText},
		},
	})
	require.NoError(t, channel.Insert())

	engine := gin.New()
	engine.Use(middleware.RequestId())
	SetRelayRouter(engine)
	SetTaskPluginProtocolRouter(engine)

	return relayInputModalityTestFixture{
		router:        engine,
		credential:    fmt.Sprintf("sk-%s-%d", tokenKey, channel.Id),
		requestModel:  "source",
		unpinnedToken: "sk-" + tokenKey,
		textChannel:   channel,
	}
}

// TestRelayRouterReturnsProtocolNativeInputModalityErrors 验证完整 HTTP 中间件和路由链返回协议原生错误。
func TestRelayRouterReturnsProtocolNativeInputModalityErrors(t *testing.T) {
	fixture := setupRelayInputModalityRouter(t, "")
	tests := []struct {
		name           string
		path           string
		body           string
		expectedModel  string
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
			modelName := test.expectedModel
			if modelName == "" {
				modelName = fixture.requestModel
			}
			assert.Contains(t, message, "model "+modelName+" has no declared image input capability")
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

func TestRelayRouterRoutesImageInputBeforePriority(t *testing.T) {
	fixture := setupRelayInputModalityRouter(t, os.Getenv("NEW_API_IMAGE_ROUTING_TEST_DSN"))
	const png = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jWZkAAAAASUVORK5CYII="
	const response = `{"id":"fixture-reply","object":"chat.completion","model":"upstream-alias","choices":[{"index":0,"message":{"role":"assistant","content":"fixture image recognized"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`
	textBody := `{"model":"source","messages":[{"role":"user","content":"hello"}]}`
	imageBody := fmt.Sprintf(`{"model":"source","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"%s"}}]}]}`, png)
	compactBody := fmt.Sprintf(`{"model":"source","input":[{"type":"input_image","image_url":"%s"}]}`, png)

	textCalls, imageCalls := make(chan string, 16), make(chan string, 16)
	upstream := func(calls chan<- string) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			calls <- string(body)
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(req.URL.Path, "/responses/compact") {
				_, _ = io.WriteString(w, `{"id":"cmp-fixture","object":"response.compaction","output":[],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}`)
				return
			}
			_, _ = io.WriteString(w, response)
		}))
		t.Cleanup(server.Close)
		return server
	}
	text, vision := upstream(textCalls), upstream(imageCalls)
	textChannel := fixture.textChannel
	textChannel.BaseURL = &text.URL
	textChannel.Priority = common.GetPointer(int64(100))
	require.NoError(t, textChannel.Update())

	imageChannel := &model.Channel{
		Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Name: "vision", Models: textChannel.Models, Group: textChannel.Group,
		Key: "fixture-vision-key", BaseURL: &vision.URL, Priority: common.GetPointer(int64(10)),
		ModelMapping: common.GetPointer(`{"source":"upstream-alias"}`),
	}
	imageSettings := dto.ChannelSettings{ModelInputModalities: types.ModelInputModalities{
		"source":                {types.InputModalityText, types.InputModalityImage},
		"source-openai-compact": {types.InputModalityText},
		"upstream-alias":        {types.InputModalityText},
	}}
	imageChannel.SetSetting(imageSettings)
	require.NoError(t, imageChannel.Insert())

	global := model_setting.GetGlobalSettings()
	previous := global.ModelInputModalities
	global.ModelInputModalities = types.ModelInputModalities{"source": {types.InputModalityText}}
	t.Cleanup(func() { global.ModelInputModalities = previous })

	send := func(path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		fixture.router.ServeHTTP(rec, req)
		return rec
	}
	chat := "/v1/chat/completions"
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache=%t", cached), func(t *testing.T) {
			common.MemoryCacheEnabled = cached
			refresh := func(channel *model.Channel) {
				t.Helper()
				require.NoError(t, model.DB.Model(channel).Select("setting", "channel_info").Updates(channel).Error)
				if cached {
					model.InitChannelCache()
				}
			}
			imageChannel.SetSetting(imageSettings)
			refresh(imageChannel)

			out := send(chat, textBody, fixture.unpinnedToken)
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			require.Len(t, textCalls, 1)
			assert.Contains(t, <-textCalls, "hello")
			assert.Empty(t, imageCalls)

			out = send(chat, imageBody, fixture.unpinnedToken)
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			assert.Contains(t, out.Body.String(), "fixture image recognized")
			require.Len(t, imageCalls, 1)
			upstreamBody := <-imageCalls
			assert.Contains(t, upstreamBody, png)
			assert.Contains(t, upstreamBody, "upstream-alias")
			assert.Empty(t, textCalls)

			out = send("/v1/responses/compact", compactBody, fixture.unpinnedToken)
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			require.Len(t, imageCalls, 1)
			assert.Contains(t, <-imageCalls, png)
			assert.Empty(t, textCalls)

			out = send(chat, imageBody, fixture.credential)
			assert.Equal(t, http.StatusBadRequest, out.Code)
			assert.Contains(t, out.Body.String(), "unsupported_input_modality")

			imageChannel.SetSetting(dto.ChannelSettings{})
			refresh(imageChannel)
			out = send(chat, imageBody, fixture.unpinnedToken)
			assert.Equal(t, http.StatusBadRequest, out.Code)
			assert.Contains(t, out.Body.String(), "unsupported_input_modality")

			global.ModelInputModalities = nil
			out = send(chat, imageBody, fixture.unpinnedToken)
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			require.Len(t, imageCalls, 1)
			assert.Contains(t, <-imageCalls, png)

			global.ModelInputModalities = types.ModelInputModalities{"source": {types.InputModalityText}}
			imageChannel.SetSetting(imageSettings)
			imageChannel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 1, MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled}}
			refresh(imageChannel)
			out = send(chat, imageBody, fixture.unpinnedToken)
			assert.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
			assert.NotContains(t, out.Body.String(), "unsupported_input_modality")
			assert.Empty(t, textCalls)
			assert.Empty(t, imageCalls)
			imageChannel.ChannelInfo = model.ChannelInfo{}
		})
	}
}
