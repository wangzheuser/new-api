package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	rootdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPRelayAttemptLogs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		retries  int
		recover  bool
		attempts int
	}{
		{"retry succeeds", 1, true, 2},
		{"five failures", 4, false, 5},
		{"no retry", 0, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialect := os.Getenv("ATTEMPT_LOG_DB")
			if dialect == "" {
				dialect = "sqlite"
			}
			db := modelManagementDB(t, dialect, os.Getenv("TEST_"+strings.ToUpper(dialect)+"_DSN"))
			require.NoError(t, db.AutoMigrate(&model.Log{}))
			require.NoError(t, db.Create(&model.User{Id: 1, Username: "fixture", Group: "default", Quota: 1000000, Status: common.UserStatusEnabled}).Error)
			oldRetry, oldMemory, oldRedis := common.RetryTimes, common.MemoryCacheEnabled, common.RedisEnabled
			oldAuto, oldErrorLog := common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled
			oldRatio := ratio_setting.ModelRatio2JSONString()
			settings := operation_setting.GetGeneralSetting()
			oldOverride := settings.DefaultFinalErrorOverride
			common.RetryTimes, common.MemoryCacheEnabled, common.RedisEnabled = tc.retries, false, false
			common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = false, true
			settings.DefaultFinalErrorOverride = finalErrorOverrideForTest("mapped final error", 503, "model_unavailable")
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"ATTEMPT_TEST":0}`))
			t.Cleanup(func() {
				common.RetryTimes, common.MemoryCacheEnabled, common.RedisEnabled = oldRetry, oldMemory, oldRedis
				common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = oldAuto, oldErrorLog
				settings.DefaultFinalErrorOverride = oldOverride
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatio))
			})
			service.InitHttpClient()
			attempts := 0
			const requestID = "attempt-log-regression"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if attempts > 1 {
					_, total, err := model.GetAllLogs(model.LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", requestID, "", true)
					assert.NoError(t, err)
					assert.Zero(t, total, "a failed attempt is not a final request result")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.recover && attempts == tc.attempts {
					_, _ = fmt.Fprint(w, `{"id":"test","object":"chat.completion","model":"ATTEMPT_TEST","choices":[{"index":0,"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
					return
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = fmt.Fprintf(w, `{"error":{"message":"raw attempt %d","type":"rate_limit_error","code":"rate_limit"}}`, attempts)
			}))
			defer upstream.Close()
			var first *model.Channel
			for i := range tc.attempts {
				channel := &model.Channel{Name: fmt.Sprintf("attempt-%d", i+1), Type: constant.ChannelTypeOpenAI, Key: "fixture", Status: common.ChannelStatusEnabled, Group: "default", Models: "ATTEMPT_TEST", BaseURL: &upstream.URL, Priority: common.GetPointer(int64(100 - i))}
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, channel.AddAbilities(db))
				if first == nil {
					first = channel
				}
			}
			router := gin.New()
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				c.Set("id", 1)
				c.Set("user_id", 1)
				c.Set("group", "default")
				c.Set("token_group", "default")
				c.Set("user_group", "default")
				c.Set(common.RequestIdKey, requestID)
				require.Nil(t, middleware.SetupContextForSelectedChannel(c, first, "ATTEMPT_TEST"))
				Relay(c, types.RelayFormatOpenAI)
			})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"ATTEMPT_TEST","messages":[{"role":"user","content":"hello"}],"max_tokens":16}`))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			require.Equal(t, tc.attempts, attempts, response.Body.String())
			failures := tc.attempts
			if tc.recover {
				failures--
				assert.Contains(t, response.Body.String(), "recovered")
			} else {
				assert.Equal(t, 503, response.Code)
				assert.Contains(t, response.Body.String(), "mapped final error")
			}
			var rows []model.Log
			require.NoError(t, db.Where("request_id = ?", requestID).Order("id").Find(&rows).Error)
			require.Len(t, rows, failures+1)
			for i, row := range rows[:failures] {
				assert.True(t, row.IsIntermediate)
				assert.Contains(t, row.Content, fmt.Sprintf("raw attempt %d", i+1))
			}
			final := rows[len(rows)-1]
			assert.False(t, final.IsIntermediate)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(final.Other, &other))
			admin := other["admin_info"].(map[string]any)
			events, ok := admin["request_policy"].([]any)
			require.True(t, ok, "final logs preserve request policy decisions")
			failureEvents := 0
			for _, item := range events {
				event := item.(map[string]any)
				if event["decision"].(map[string]any)["action"] == "failure" {
					failureEvents++
				}
			}
			assert.Equal(t, failures, failureEvents)
			assert.Len(t, admin["use_channel"], tc.attempts)
			if !tc.recover {
				assert.Contains(t, final.Content, "mapped final error")
				assert.Equal(t, "attempt_budget_exhausted", events[len(events)-1].(map[string]any)["decision"].(map[string]any)["reason"])
			}
		})
	}
}

// TestRelayClientErrorLogContent verifies that final logs use the structured client-facing error.
func TestRelayClientErrorLogContent(t *testing.T) {
	t.Run("OpenAI structured error", func(t *testing.T) {
		err := types.WithOpenAIError(types.OpenAIError{
			Message: "最终响应",
			Type:    "upstream_error",
			Code:    "upstream_error",
		}, http.StatusServiceUnavailable)
		err.SetMessage("internal wrapper (request id: req-test)")

		require.Equal(t, "status_code=503, 最终响应", relayClientErrorLogContent(err, types.RelayFormatOpenAI))
	})

	t.Run("Claude structured error", func(t *testing.T) {
		err := types.WithClaudeError(types.ClaudeError{
			Message: "最终响应",
			Type:    "upstream_error",
		}, http.StatusServiceUnavailable)
		err.SetMessage("internal wrapper (request id: req-test)")

		require.Equal(t, "status_code=503, 最终响应", relayClientErrorLogContent(err, types.RelayFormatClaude))
	})

	t.Run("local error includes request id", func(t *testing.T) {
		err := types.NewErrorWithStatusCode(errors.New("最终响应"), types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)
		err.SetMessage("最终响应 (request id: req-test)")

		require.Equal(
			t,
			"status_code=503, 最终响应 (request id: req-test)",
			relayClientErrorLogContent(err, types.RelayFormatOpenAI),
		)
	})
}

// TestRecordRelayErrorLogPersistsIntermediateState protects final-only queries from in-flight retries.
func TestRecordRelayErrorLogPersistsIntermediateState(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	previousErrorLogEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		constant.ErrorLogEnabled = previousErrorLogEnabled
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 1)
	ctx.Set("username", "test-user")
	ctx.Set("original_model", "test-model")
	ctx.Set(common.RequestIdKey, "req-intermediate")

	relayError := types.NewOpenAIError(
		errors.New("upstream unavailable"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
	)
	recordRelayErrorLog(ctx, nil, relayError, "", nil, true)

	ctx.Set(common.RequestIdKey, "req-terminal")
	recordRelayErrorLog(ctx, nil, relayError, "", nil, false)

	var logs []model.Log
	require.NoError(t, db.Order("id").Find(&logs).Error)
	require.Len(t, logs, 2)
	require.True(t, logs[0].IsIntermediate)
	require.False(t, logs[1].IsIntermediate)
}

// TestRecordRelayErrorLogKeepsAttemptIdentity preserves transport evidence across public mapping.
func TestRecordRelayErrorLogKeepsAttemptIdentity(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	previous := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() { constant.ErrorLogEnabled = previous })
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Set("id", 1)
	ctx.Set("original_model", "client-alias")
	ctx.Set(common.RequestIdKey, "transport-evidence")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "actual-model"}}
	raw := types.NewErrorWithStatusCode(errors.New("upstream unavailable"), types.ErrorCodeBadResponseStatusCode, 503, types.ErrOptionWithUpstreamStatusCode(503))
	public := types.NewErrorWithStatusCode(errors.New("mapped error"), "public_error", 502)
	recordRelayErrorLog(ctx, info, public, "", raw, false)
	var entry model.Log
	require.NoError(t, db.First(&entry).Error)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(entry.Other, &other))
	assert.Equal(t, "actual-model", other["upstream_model_name"])
	admin := other["admin_info"].(map[string]interface{})
	assert.Equal(t, float64(503), admin["upstream_status_code"])
	assert.Equal(t, float64(502), other["status_code"])
	ctx.Set(common.RequestIdKey, "local-public-503")
	recordRelayErrorLog(ctx, nil, types.NewErrorWithStatusCode(errors.New("local failure"), types.ErrorCodeDoRequestFailed, 503), "", nil, false)
	var local model.Log
	require.NoError(t, db.Where("request_id = ?", "local-public-503").First(&local).Error)
	var localOther map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(local.Other, &localOther))
	assert.NotContains(t, localOther["admin_info"], "upstream_status_code")
}

// TestRecordRelayErrorLogPreservesDiscardedResponseOverrideAttempt verifies a
// failed attempt keeps its response-stage skip reason before retry state resets.
func TestRecordRelayErrorLogPreservesDiscardedResponseOverrideAttempt(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	previousErrorLogEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		constant.ErrorLogEnabled = previousErrorLogEnabled
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 1)
	ctx.Set("username", "test-user")
	ctx.Set("original_model", "test-model")
	ctx.Set(common.RequestIdKey, "req-response-override-attempt")
	decision := &relaycommon.ResponseOverrideDecision{
		Configured:       true,
		NotAppliedReason: relaycommon.ResponseOverrideNotAppliedRelayError,
		Billable:         true,
	}
	info := &relaycommon.RelayInfo{ResponseOverride: decision}
	relayError := types.NewOpenAIError(
		errors.New("upstream unavailable"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
	)

	recordRelayErrorLog(ctx, info, relayError, "", nil, true)

	var log model.Log
	require.NoError(t, db.First(&log).Error)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	responseOverride, ok := adminInfo["response_override"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, responseOverride["configured"])
	assert.Equal(t, relaycommon.ResponseOverrideNotAppliedRelayError, responseOverride["not_applied_reason"])
}

func TestRecordRelayErrorLogPersistsProtocolRouteAndNormalizationAudit(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	previousErrorLogEnabled := constant.ErrorLogEnabled
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		constant.ErrorLogEnabled = previousErrorLogEnabled
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Set("id", 1)
	ctx.Set("username", "test-user")
	ctx.Set("original_model", "MODEL_X")
	ctx.Set("channel_id", 54)
	ctx.Set(common.RequestIdKey, "req-protocol-audit")
	info := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatClaude},
		FinalRequestRelayFormat: types.RelayFormatClaude,
		ChannelRoutePlan: &types.ChannelRoutePlan{
			RouteMode:           types.ChannelRouteModeNormalized,
			ClientRelayFormat:   types.RelayFormatClaude,
			UpstreamRelayFormat: types.RelayFormatClaude,
			UpstreamPath:        "/v1/messages",
			RequestNormalizer:   "anthropic_messages_compatible",
			CapabilitySource:    "model_override",
		},
		ProtocolNormalization: &types.ProtocolNormalizationAudit{
			Normalizer:                    "anthropic_messages_compatible",
			ReasoningOnlyAssistantDropped: 1,
			EmptyAssistantMessagesDropped: 1,
			ToolIDsNormalized:             2,
		},
	}
	relayError := types.NewOpenAIError(
		errors.New("upstream rejected request"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusBadRequest,
	)

	recordRelayErrorLog(ctx, info, relayError, "", nil, false)

	var log model.Log
	require.NoError(t, db.First(&log).Error)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	protocolRoute, ok := adminInfo["protocol_route"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, string(types.ChannelRouteModeNormalized), protocolRoute["mode"])
	assert.Equal(t, "anthropic_messages_compatible", protocolRoute["request_normalizer"])
	protocolNormalization, ok := adminInfo["protocol_normalization"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(1), protocolNormalization["reasoning_only_assistant_dropped"])
	assert.Equal(t, float64(1), protocolNormalization["empty_assistant_messages_dropped"])
	assert.Equal(t, float64(2), protocolNormalization["tool_ids_normalized"])
	assert.Equal(t, []interface{}{"Claude Messages"}, other["request_conversion"])
	assert.Equal(t, string(types.RelayFormatClaude), other["final_request_relay_format"])
}

// TestResolveConfiguredFinalRelayError verifies channel precedence, system fallback, and leak-safe fallback.
func TestResolveConfiguredFinalRelayError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	settings := operation_setting.GetGeneralSetting()
	previous := settings.DefaultFinalErrorOverride
	t.Cleanup(func() {
		settings.DefaultFinalErrorOverride = previous
	})
	settings.DefaultFinalErrorOverride = finalErrorOverrideForTest("系统错误", 503, "default_error")

	relayInfo := &relaycommon.RelayInfo{
		LastError: types.WithOpenAIError(types.OpenAIError{
			Message: "raw upstream error",
			Code:    "upstream_error",
		}, http.StatusServiceUnavailable),
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: finalErrorOverrideForTest("渠道错误", 502, "channel_error"),
		},
	}

	mapped := resolveConfiguredFinalRelayError(ctx, relayInfo)
	require.Equal(t, http.StatusBadGateway, mapped.StatusCode)
	require.Equal(t, types.ErrorCode("channel_error"), mapped.GetErrorCode())
	require.Equal(t, "渠道错误", mapped.ToOpenAIError().Message)

	relayInfo.ChannelMeta.ParamOverride = nil
	mapped = resolveConfiguredFinalRelayError(ctx, relayInfo)
	require.Equal(t, http.StatusServiceUnavailable, mapped.StatusCode)
	require.Equal(t, types.ErrorCode("default_error"), mapped.GetErrorCode())
	require.Equal(t, "系统错误", mapped.ToOpenAIError().Message)

	settings.DefaultFinalErrorOverride = nil
	mapped = resolveConfiguredFinalRelayError(ctx, relayInfo)
	require.Equal(t, http.StatusBadGateway, mapped.StatusCode)
	require.Equal(t, types.ErrorCodeBadResponse, mapped.GetErrorCode())
	require.Equal(t, http.StatusText(http.StatusBadGateway), mapped.ToOpenAIError().Message)
}

func finalErrorOverrideForTest(message string, statusCode int, code string) map[string]interface{} {
	return map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "final_error",
				"mode":  "return_error",
				"value": map[string]interface{}{
					"message":     message,
					"status_code": statusCode,
					"code":        code,
					"type":        "new_api_error",
				},
			},
		},
	}
}

func TestHTTPRelayProtocolFallbackLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status           int
		message          string
		enabled, allFail bool
		retries, want    int
		multiKey         bool
	}{
		{"explicit refusal", 400, "This model is only supported on /v1/responses", true, false, 2, 2, false},
		{"multi-key fallback", 400, "This model is only supported on /v1/responses", true, false, 2, 2, true},
		{"explicit endpoint", 404, "Model not supported on this endpoint", true, false, 2, 2, false},
		{"disabled", 400, "This model is only supported on /v1/responses", false, false, 2, 1, false},
		{"ambiguous not found", 404, "model does not exist", true, false, 0, 1, false},
		{"authentication", 401, "invalid credentials", true, false, 0, 1, false},
		{"rate limit", 429, "rate limit exceeded", true, false, 0, 1, false},
		{"budget exhausted", 400, "This model is only supported on /v1/responses", true, false, 0, 1, false},
		{"all protocols refused", 400, "Model not supported on this endpoint", true, true, 4, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialect := os.Getenv("ATTEMPT_LOG_DB")
			if dialect == "" {
				dialect = "sqlite"
			}
			db := modelManagementDB(t, dialect, os.Getenv("TEST_"+strings.ToUpper(dialect)+"_DSN"))
			require.NoError(t, db.AutoMigrate(&model.Log{}, &model.UserSubscription{}))
			require.NoError(t, db.Create(&model.User{Id: 1, Username: "fixture", Group: "default", Quota: 1000000, Status: common.UserStatusEnabled}).Error)
			oldRetry, oldMemory, oldRedis := common.RetryTimes, common.MemoryCacheEnabled, common.RedisEnabled
			oldRatio := ratio_setting.ModelRatio2JSONString()
			common.RetryTimes, common.MemoryCacheEnabled, common.RedisEnabled = tc.retries, false, false
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"PROTOCOL_TEST":0}`))
			t.Cleanup(func() {
				common.RetryTimes, common.MemoryCacheEnabled, common.RedisEnabled = oldRetry, oldMemory, oldRedis
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatio))
			})
			service.InitHttpClient()
			paths := []string{}
			keys := []string{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				keys = append(keys, r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/chat/completions" || tc.allFail {
					w.WriteHeader(tc.status)
					fmt.Fprintf(w, `{"error":{"message":%q,"type":"invalid_request_error"}}`, tc.message)
					return
				}
				fmt.Fprint(w, `{"id":"resp_fixture","object":"response","model":"PROTOCOL_TEST","status":"completed","output":[{"id":"msg_fixture","type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered","annotations":[]}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`)
			}))
			defer upstream.Close()
			channel := &model.Channel{Name: "protocol-fixture", Type: 1, Key: "fixture", Status: common.ChannelStatusEnabled, Group: "default", Models: "PROTOCOL_TEST", BaseURL: &upstream.URL}
			if tc.multiKey {
				channel.Key = "fixture-a\nfixture-b"
				channel.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModePolling}
			}
			channel.SetSetting(rootdto.ChannelSettings{ProtocolPolicy: &rootdto.ChannelProtocolPolicy{Native: map[constant.EndpointType]rootdto.ProtocolCapability{constant.EndpointTypeOpenAI: {NonStream: true}, constant.EndpointTypeOpenAIResponse: {NonStream: true}}, AutoConvert: true, RuntimeFallback: tc.enabled}})
			require.NoError(t, db.Create(channel).Error)
			require.NoError(t, channel.AddAbilities(db))
			require.NoError(t, service.ClearProtocolObservations(context.Background(), channel.Id))
			router := gin.New()
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				for _, key := range []string{"id", "user_id"} {
					c.Set(key, 1)
				}
				for _, key := range []string{"group", "token_group", "user_group"} {
					c.Set(key, "default")
				}
				require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, "PROTOCOL_TEST"))
				Relay(c, types.RelayFormatOpenAI)
			})
			send := func() *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"PROTOCOL_TEST","messages":[{"role":"user","content":"hello"}]}`))
				r.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(w, r)
				return w
			}
			response := send()
			assert.Len(t, paths, tc.want, response.Body.String())
			if tc.enabled && !tc.allFail && tc.want == 2 {
				require.Equal(t, 200, response.Code, response.Body.String())
				assert.Contains(t, response.Body.String(), "recovered")
				if tc.multiKey {
					require.Len(t, keys, 2)
					assert.Equal(t, keys[0], keys[1], "protocol fallback must retain the current credential")
					return
				}
				paths = nil
				second := send()
				require.Equal(t, 200, second.Code, second.Body.String())
				assert.Equal(t, []string{"/v1/responses"}, paths)
			}
		})
	}
}

func TestModelProtocolExceptionRoundTrip(t *testing.T) {
	kind := os.Getenv("ATTEMPT_LOG_DB")
	if kind == "" {
		kind = "sqlite"
	}
	db, _ := newAuditTestDatabase(t, kind, os.Getenv("TEST_"+strings.ToUpper(kind)+"_DSN"))
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	endpoint := constant.EndpointTypeOpenAI
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Name: "protocol-exception", Key: "fixture"}
	channel.SetSetting(rootdto.ChannelSettings{ProtocolPolicy: &rootdto.ChannelProtocolPolicy{
		Native:         map[constant.EndpointType]rootdto.ProtocolCapability{endpoint: {NonStream: true, Stream: true}},
		ModelOverrides: map[string]rootdto.ModelProtocolProfile{"sample": {Overrides: map[constant.EndpointType]rootdto.ProtocolCapabilityPatch{endpoint: {Stream: common.GetPointer(false)}}}},
	}})
	require.NoError(t, db.Create(&channel).Error)
	var read model.Channel
	require.NoError(t, db.First(&read, channel.Id).Error)
	settings, err := read.ReadSetting()
	require.NoError(t, err)
	require.NotNil(t, settings.ProtocolPolicy)
	require.NoError(t, settings.ProtocolPolicy.Validate())
	for _, tc := range []struct {
		model     string
		stream    bool
		available bool
	}{{"sample", true, false}, {"sample", false, true}, {"other", true, true}} {
		plan, err := service.PlanChannelProtocolRoute(&read, tc.model, "/v1/chat/completions", tc.stream)
		require.NoError(t, err)
		assert.Equal(t, tc.available, plan != nil)
	}
}
