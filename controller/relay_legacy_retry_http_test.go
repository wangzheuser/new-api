package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLegacyClaudeMultiKeyHTTP exercises retry, key isolation, and billing through the real relay.
func TestLegacyClaudeMultiKeyHTTP(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, format := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI} {
		for _, scenario := range []struct {
			name          string
			status        int
			message       string
			recover       bool
			backup        bool
			wantKeys      []string
			dialFailure   bool
			headerTimeout bool
			flushHeaders  bool
			bodyTimeout   bool
			cache         bool
			responses     bool
			stop          string
			searchOptions bool
			injectSearch  bool
			sseError      string
		}{
			{name: "Responses HTTP 429", responses: true, status: 429, message: "quota exceeded", recover: true, wantKeys: []string{"KEY_A", "KEY_B"}},
			{name: "Responses first SSE rejection", responses: true, sseError: "overloaded_error", message: "busy", recover: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A"}},
			{name: "Responses SSE key quota", responses: true, sseError: "rate_limit_error", message: "quota exceeded", recover: true, wantKeys: []string{"KEY_A", "KEY_B"}},
			{name: "Responses last attempt SSE quota", responses: true, sseError: "rate_limit_error", message: "quota exceeded", stop: "budget", wantKeys: []string{"KEY_A"}},
			{name: "Responses provider cache SSE quota", responses: true, sseError: "rate_limit_error", message: "quota exceeded", stop: "provider_state", backup: true, wantKeys: []string{"KEY_A"}},
			{name: "Responses hosted HTTP timeout", responses: true, status: 504, searchOptions: true, backup: true, wantKeys: []string{"KEY_A"}},
			{name: "Responses plain HTTP timeout", responses: true, status: 504, recover: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A"}},
			{name: "Responses overridden hosted timeout", responses: true, status: 524, injectSearch: true, backup: true, wantKeys: []string{"KEY_A"}},
			{name: "Responses nonstream read exclusion", responses: true, bodyTimeout: true, injectSearch: true, backup: true, wantKeys: []string{"KEY_A"}},
			{name: "Responses nonstream hosted transport timeout", responses: true, bodyTimeout: true, stop: "transport", injectSearch: true, backup: true, wantKeys: []string{"KEY_A"}},
			{name: "Responses nonstream plain transport timeout", responses: true, bodyTimeout: true, stop: "transport", recover: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A"}},
			{name: "Chat hosted first payload timeout", headerTimeout: true, searchOptions: true, backup: true, wantKeys: []string{"KEY_A"}},
			{name: "Responses hosted timeout after headers", responses: true, headerTimeout: true, flushHeaders: true, injectSearch: true, backup: true, wantKeys: []string{"KEY_A"}},
			{name: "plain timeout after headers", headerTimeout: true, flushHeaders: true, recover: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A"}},
			{name: "pinned channel rotates keys", stop: "same_channel", status: 429, message: "quota exceeded", recover: true, wantKeys: []string{"KEY_A", "KEY_B"}},
			{name: "pinned channel cannot use backup", stop: "same_channel", status: 500, message: "upstream failed", backup: true, wantKeys: []string{"KEY_A"}},
			{name: "key quota recovery", status: 429, message: "quota exceeded", recover: true, wantKeys: []string{"KEY_A", "KEY_B"}},
			{name: "key quota budget exhausted", status: 429, message: "quota exceeded", wantKeys: []string{"KEY_A", "KEY_B", "KEY_C"}},
			{name: "channel recovery", status: 500, message: "upstream failed", recover: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A"}},
			{name: "channel unavailable", status: 500, message: "upstream failed", wantKeys: []string{"KEY_A"}},
			{name: "protocol event", wantKeys: []string{"KEY_A"}},
			{name: "metadata without payload", stop: "payload", wantKeys: []string{"KEY_A"}},
			{name: "first payload recovery", headerTimeout: true, recover: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A"}},
			{name: "first payload cached recovery", headerTimeout: true, cache: true, recover: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A"}},
			{name: "first payload all timeout", headerTimeout: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A", "BACKUP_B"}},
			{name: "first payload no backup", headerTimeout: true, wantKeys: []string{"KEY_A"}},
			{name: "first payload budget", headerTimeout: true, backup: true, stop: "budget", wantKeys: []string{"KEY_A"}},
			{name: "first payload pinned", headerTimeout: true, stop: "pin", wantKeys: []string{"KEY_A"}},
			{name: "first payload strict", headerTimeout: true, stop: "session", wantKeys: []string{"KEY_A"}},
			{name: "first payload excluded", headerTimeout: true, stop: "status", wantKeys: []string{"KEY_A"}},
			{name: "client cancels before headers", stop: "client", wantKeys: []string{"KEY_A"}},
			{name: "dial recovery", dialFailure: true, recover: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A"}},
			{name: "dial budget exhausted", dialFailure: true, backup: true, wantKeys: []string{"KEY_A", "BACKUP_A", "BACKUP_B"}},
			{name: "dial no budget", dialFailure: true, stop: "budget", wantKeys: []string{"KEY_A"}},
			{name: "dial status excluded", dialFailure: true, stop: "status", wantKeys: []string{"KEY_A"}},
			{name: "dial pinned", dialFailure: true, stop: "pin", wantKeys: []string{"KEY_A"}},
			{name: "dial strict session", dialFailure: true, stop: "session", wantKeys: []string{"KEY_A"}},
		} {
			if scenario.responses && format != types.RelayFormatClaude || scenario.searchOptions && !scenario.responses && format != types.RelayFormatOpenAI {
				continue
			}
			entryFormat := format
			if scenario.responses {
				entryFormat = types.RelayFormatOpenAIResponses
			}
			t.Run(string(entryFormat)+"/"+scenario.name, func(t *testing.T) {
				if scenario.responses {
					old := constant.ErrorLogEnabled
					constant.ErrorLogEnabled = true
					t.Cleanup(func() { constant.ErrorLogEnabled = old })
				}
				oldDB, oldLogDB := model.DB, model.LOG_DB
				oldRedis, oldRedisEnabled := common.RDB, common.RedisEnabled
				oldRetry, oldAuto, oldMemory := common.RetryTimes, common.AutomaticDisableChannelEnabled, common.MemoryCacheEnabled
				oldRatio := ratio_setting.ModelRatio2JSONString()
				db := setupModelListControllerTestDB(t)
				require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Token{}))
				const initialQuota = 100000
				require.NoError(t, db.Create(&model.User{Id: 1, Username: "fixture", Role: common.RoleRootUser, Quota: initialQuota, Status: common.UserStatusEnabled, Group: "default"}).Error)
				require.NoError(t, db.Create(&model.Token{Id: 1, UserId: 1, Key: "fixture-token", RemainQuota: initialQuota}).Error)
				redisServer, err := miniredis.Run()
				require.NoError(t, err)
				client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
				common.RDB, common.RedisEnabled = client, true
				common.RetryTimes, common.AutomaticDisableChannelEnabled, common.MemoryCacheEnabled = 2, true, false
				t.Cleanup(func() {
					_ = client.Close()
					redisServer.Close()
					model.DB, model.LOG_DB = oldDB, oldLogDB
					common.RDB, common.RedisEnabled = oldRedis, oldRedisEnabled
					common.RetryTimes, common.AutomaticDisableChannelEnabled, common.MemoryCacheEnabled = oldRetry, oldAuto, oldMemory
					require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatio))
				})
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"MODEL_X":1}`))
				service.InitHttpClient()
				previousRanges := operation_setting.AutomaticRetryStatusCodeRanges
				operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 400, End: 599}}
				t.Cleanup(func() { operation_setting.AutomaticRetryStatusCodeRanges = previousRanges })
				if scenario.stop == "budget" {
					common.RetryTimes = 0
				}
				if scenario.stop == "status" {
					operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 503, End: 503}}
				}
				if scenario.stop == "payload" || scenario.headerTimeout {
					previous := common.GetRelayFirstPayloadTimeout()
					common.SetRelayFirstPayloadTimeout(150 * time.Millisecond)
					t.Cleanup(func() { common.SetRelayFirstPayloadTimeout(previous) })
				}
				var mu sync.Mutex
				var attempts []string
				requestCtx, cancel := context.WithCancel(context.Background())
				defer cancel()
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					key := r.Header.Get("X-Api-Key")
					if scenario.searchOptions || scenario.injectSearch {
						body, err := io.ReadAll(r.Body)
						assert.NoError(t, err)
						assert.Contains(t, string(body), "web_search_20250305")
					}
					if scenario.bodyTimeout {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"fixture-ok"}],"model":"MODEL_X","stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`)
						return
					}
					if scenario.headerTimeout && (key == "KEY_A" || !scenario.recover) {
						if key == "KEY_A" {
							common.SetRelayFirstPayloadTimeout(time.Hour)
						}
						_, _ = io.Copy(io.Discard, r.Body)
						if scenario.flushHeaders {
							w.Header().Set("Content-Type", "text/event-stream")
							w.(http.Flusher).Flush()
						}
						select {
						case <-r.Context().Done():
						case <-time.After(3 * time.Second):
							t.Error("upstream header wait was not cancelled")
						}
						return
					}
					failed := scenario.status != 0 || scenario.sseError != ""
					if scenario.recover {
						failed = key == "KEY_A"
						if scenario.backup {
							failed = key != "BACKUP_A"
						}
					}
					if failed && scenario.sseError != "" {
						w.Header().Set("Content-Type", "text/event-stream")
						w.Header().Set("Retry-After", "600")
						_, _ = fmt.Fprintf(w, "data: {\"type\":\"error\",\"error\":{\"type\":%q,\"message\":%q}}\n\n", scenario.sseError, scenario.message)
						return
					}
					if failed {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(scenario.status)
						_, _ = fmt.Fprintf(w, `{"error":{"message":%q,"type":"api_error"}}`, scenario.message)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"MODEL_X\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n")
					if scenario.name == "protocol event" {
						_, _ = io.WriteString(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"stream failed\"}}\n\n")
						return
					}
					if scenario.stop == "payload" {
						w.(http.Flusher).Flush()
						select {
						case <-r.Context().Done():
						case <-time.After(3 * time.Second):
							t.Error("metadata-only upstream was not cancelled")
						}
						return
					}
					_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"fixture-ok\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
				}))
				defer upstream.Close()
				clientTransport := service.GetHttpClient()
				base := clientTransport.Transport
				transport := base.(*http.Transport).Clone()
				t.Cleanup(transport.CloseIdleConnections)
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if strings.HasPrefix(address, "unreachable.invalid:") {
						return nil, &net.OpError{Op: "dial", Net: network, Err: context.DeadlineExceeded}
					}
					return (&net.Dialer{}).DialContext(ctx, network, address)
				}
				transport.Proxy = nil
				clientTransport.Transport = legacyRetryTransportFunc(func(r *http.Request) (*http.Response, error) {
					mu.Lock()
					attempts = append(attempts, r.Header.Get("X-Api-Key"))
					mu.Unlock()
					if scenario.stop == "client" {
						cancel()
						return nil, r.Context().Err()
					}
					resp, err := transport.RoundTrip(r)
					if err == nil && scenario.bodyTimeout && r.Header.Get("X-Api-Key") == "KEY_A" {
						_ = resp.Body.Close()
						timeout := fmt.Errorf("upstream response: %w", context.DeadlineExceeded)
						if scenario.stop == "transport" {
							return nil, timeout
						}
						resp.Body = io.NopCloser(iotest.ErrReader(timeout))
					}
					return resp, err
				})
				t.Cleanup(func() { clientTransport.Transport = base })
				primaryURL := upstream.URL
				if scenario.dialFailure {
					primaryURL = "http://unreachable.invalid"
				}

				channel := &model.Channel{
					Type: constant.ChannelTypeAnthropic, Name: "legacy-fixture", Key: "KEY_A\nKEY_B\nKEY_C\nKEY_D", Status: common.ChannelStatusEnabled,
					AutoBan: common.GetPointer(1), BaseURL: &primaryURL, Priority: common.GetPointer(int64(3)), Group: "default", Models: "MODEL_X",
					ChannelInfo:   model.ChannelInfo{IsMultiKey: true, MultiKeySize: 4, MultiKeyMode: constant.MultiKeyModePolling},
					OtherSettings: `{"multi_key_auto_disable_override":{"temporary_status_codes":"429,500-503","persistent_status_codes":"401","temporary_disable_minutes":1}}`,
				}
				if scenario.injectSearch {
					channel.ParamOverride = common.GetPointer(`{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
				}
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "MODEL_X", ChannelId: channel.Id, Enabled: true, Priority: channel.Priority}).Error)
				if scenario.backup {
					backup := channel.Snapshot()
					backup.Id, backup.Name, backup.Key = 0, "backup-fixture", "BACKUP_A\nBACKUP_B"
					backup.ChannelInfo.MultiKeySize = 2
					if scenario.headerTimeout {
						backup.Key = "BACKUP_A"
						backup.ChannelInfo = model.ChannelInfo{}
					}
					backup.Priority = common.GetPointer(int64(2))
					if scenario.recover {
						backup.BaseURL = &upstream.URL
					}
					require.NoError(t, db.Create(backup).Error)
					require.NoError(t, backup.AddAbilities(nil))
					if scenario.name == "dial budget exhausted" || scenario.name == "first payload all timeout" || (scenario.headerTimeout && scenario.recover) {
						last := backup.Snapshot()
						last.Id, last.Name, last.Key = 0, "last-fixture", "BACKUP_B"
						last.Priority = common.GetPointer(int64(1))
						last.ChannelInfo = model.ChannelInfo{}
						require.NoError(t, db.Create(last).Error)
						require.NoError(t, last.AddAbilities(nil))
					}
				}
				if scenario.cache {
					common.MemoryCacheEnabled = true
					model.InitChannelCache()
				}
				path := "/v1/messages"
				if format == types.RelayFormatOpenAI {
					path = "/v1/chat/completions"
				}
				if scenario.responses {
					path = "/v1/responses"
				}
				var requestContext *gin.Context
				router := gin.New()
				router.POST(path, func(c *gin.Context) {
					c.Set(common.RequestIdKey, "fixture-request")
					c.Set("id", 1)
					c.Set("user_id", 1)
					c.Set("group", "default")
					c.Set("token_group", "default")
					c.Set("user_group", "default")
					common.SetContextKey(c, constant.ContextKeyTokenId, 1)
					common.SetContextKey(c, constant.ContextKeyTokenKey, "fixture-token")
					if apiErr := middleware.SetupContextForSelectedChannel(c, channel, "MODEL_X"); apiErr != nil {
						c.Status(503)
						return
					}
					if scenario.stop == "pin" {
						service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: channel.Id, RetryMode: dto.PinRetrySingleAttempt})
					}
					if scenario.stop == "same_channel" {
						service.GetChannelConstraints(c).AddPin(dto.ChannelPin{ChannelId: channel.Id, RetryMode: dto.PinRetrySameChannel})
					}
					if scenario.stop == "session" {
						c.Set("channel_affinity_skip_retry_on_failure", true)
					}
					requestContext = c
					Relay(c, entryFormat)
				})
				// Wait for the handler before inspecting stream output and settled consumption.
				response := httptest.NewRecorder()
				clientBody := `{"model":"MODEL_X","messages":[{"role":"user","content":"hello"}],"max_tokens":16,"stream":true}`
				if scenario.responses {
					clientBody = `{"model":"MODEL_X","input":"hello","stream":true}`
				}
				if scenario.searchOptions {
					if scenario.responses {
						clientBody = `{"model":"MODEL_X","input":"hello","stream":true,"tools":[{"type":"web_search_preview"}]}`
					} else {
						clientBody = `{"model":"MODEL_X","messages":[{"role":"user","content":"hello"}],"stream":true,"web_search_options":{"search_context_size":"low"}}`
					}
				}
				if scenario.bodyTimeout {
					clientBody = `{"model":"MODEL_X","input":"hello","stream":false}`
				}
				if scenario.stop == "provider_state" {
					clientBody = `{"model":"MODEL_X","input":"hello","stream":true,"cachedContent":"cachedContents/1"}`
				}
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(clientBody))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(response, request.WithContext(requestCtx))
				body := response.Body.String()
				mu.Lock()
				got := append([]string(nil), attempts...)
				mu.Unlock()
				require.Equal(t, scenario.wantKeys, got, body)
				if scenario.stop == "provider_state" {
					events := service.RequestPolicy(requestContext).Events()
					require.NotEmpty(t, events)
					assert.Equal(t, "provider_bound_state", events[len(events)-1].Decision.Reason)
				}
				if scenario.searchOptions || scenario.injectSearch {
					events := service.RequestPolicy(requestContext).Events()
					require.NotEmpty(t, events)
					reason := "non_retryable_error"
					if scenario.bodyTimeout && scenario.stop != "transport" {
						reason = "system_retry_exclusion"
					}
					assert.Equal(t, reason, events[len(events)-1].Decision.Reason)
				}
				if scenario.stop == "payload" {
					require.NoError(t, requestCtx.Err())
					assert.Contains(t, body, "first stream payload")
					events := service.RequestPolicy(requestContext).Events()
					require.NotEmpty(t, events)
					assert.Equal(t, "client_response_committed", events[len(events)-1].Decision.Reason)
				}
				if scenario.dialFailure {
					events := service.RequestPolicy(requestContext).Events()
					require.GreaterOrEqual(t, len(events), 3)
					assert.Equal(t, "transport", events[1].ErrorSource)
					assert.Equal(t, "do_request_failed", events[1].ErrorCode)
					assert.Equal(t, 500, events[1].Status)
					reasons := map[string]string{"budget": "attempt_budget_exhausted", "status": "status_not_retryable", "pin": "pinned_channel", "session": "strict_session"}
					reason := "retry_status_matched"
					if scenario.stop != "" {
						reason = reasons[scenario.stop]
					}
					assert.Equal(t, reason, events[2].Decision.Reason)
					assert.NotContains(t, body, "unreachable.invalid")
				}
				var logs []model.Log
				require.NoError(t, db.Where("type = ?", model.LogTypeConsume).Find(&logs).Error)
				var user model.User
				var token model.Token
				require.NoError(t, db.First(&user, 1).Error)
				require.NoError(t, db.First(&token, 1).Error)
				if scenario.responses {
					finalErrors, _, err := model.GetAllLogs(model.LogTypeError, 0, 0, "", "", "", 0, 10, 0, "", "fixture-request", "", true)
					require.NoError(t, err)
					if scenario.recover {
						assert.Empty(t, finalErrors)
					} else {
						require.Len(t, finalErrors, 1)
						assert.False(t, finalErrors[0].IsIntermediate)
					}
				}
				if scenario.recover {
					if scenario.responses && !scenario.bodyTimeout {
						assert.Contains(t, body, "fixture-ok")
						assert.Equal(t, 1, strings.Count(body, "event: response.created"))
						assert.Equal(t, 1, strings.Count(body, "event: response.completed"))
						var failure model.Log
						require.NoError(t, db.Where(&model.Log{Type: model.LogTypeError, IsIntermediate: true}).First(&failure).Error)
						var details struct {
							Admin struct {
								HTTPStatus int    `json:"upstream_status_code"`
								Stream     string `json:"stream_retry_state"`
							} `json:"admin_info"`
						}
						require.NoError(t, common.UnmarshalJsonStr(failure.Other, &details))
						if scenario.status != 0 {
							assert.Equal(t, scenario.status, details.Admin.HTTPStatus)
							assert.Equal(t, "headers_only", details.Admin.Stream)
						} else {
							assert.Equal(t, http.StatusOK, details.Admin.HTTPStatus)
							assert.Equal(t, "initial_rejection", details.Admin.Stream)
						}
					} else {
						assert.Equal(t, 1, strings.Count(body, "fixture-ok"), body)
					}
					assert.NotContains(t, body, `"type":"error"`)
					require.Len(t, logs, 1)
					assert.Positive(t, logs[0].Quota)
					assert.Equal(t, initialQuota-logs[0].Quota, user.Quota)
					assert.Equal(t, user.Quota, token.RemainQuota)
					if entryFormat == types.RelayFormatClaude {
						assert.Equal(t, 1, strings.Count(body, "event: message_stop"))
					} else if !scenario.responses {
						assert.Equal(t, 1, strings.Count(body, "[DONE]"))
					}
				} else {
					assert.Len(t, logs, 0)
					// Refunds run asynchronously; verify the actual wallet and token balances.
					require.Eventually(t, func() bool {
						return db.First(&user, 1).Error == nil && db.First(&token, 1).Error == nil &&
							user.Quota == initialQuota && token.RemainQuota == initialQuota
					}, time.Second, time.Millisecond)
					keys, err := client.Keys(context.Background(), "perf:MODEL_X:*").Result()
					require.NoError(t, err)
					if scenario.stop == "client" {
						assert.Empty(t, keys, "a downstream cancellation is not a health failure")
						assert.NotContains(t, body, `"error":`)
					} else {
						if scenario.bodyTimeout {
							assert.GreaterOrEqual(t, response.Code, 400, body)
							assert.Contains(t, body, `"error"`)
						} else if scenario.responses {
							assert.Equal(t, 1, strings.Count(body, "event: error\n"), body)
							if scenario.sseError == "rate_limit_error" {
								assert.Contains(t, body, `"code":"rate_limit_error"`)
							}
						} else {
							assert.Equal(t, 1, strings.Count(body, `"error":`), body)
						}
						if scenario.name == "dial no budget" {
							assert.NotEmpty(t, keys, "a transport failure is still recorded")
						}
					}
					assert.NotContains(t, body, "event: message_stop")
					assert.NotContains(t, body, "[DONE]")
				}
				if scenario.status == http.StatusTooManyRequests || scenario.sseError == "rate_limit_error" {
					assert.NotEmpty(t, service.LoadMultiKeyCooldowns(channel.Id, "KEY_A"), "key quota failures must isolate the affected credential")
					if scenario.sseError != "" {
						cooldowns := service.LoadMultiKeyCooldowns(channel.Id, "KEY_A")
						require.Len(t, cooldowns, 1)
						assert.Equal(t, http.StatusTooManyRequests, cooldowns[0].StatusCode)
						assert.GreaterOrEqual(t, cooldowns[0].RecoverAt, time.Now().Unix()+590)
					}
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ctx.Request = httptest.NewRequest(http.MethodPost, path, nil)
					key, _, apiErr := service.SelectChannelKeyForRequest(ctx, channel, "MODEL_X", nil)
					require.Nil(t, apiErr)
					failedKeys := got
					if scenario.recover {
						failedKeys = got[:len(got)-1]
					}
					assert.NotContains(t, failedKeys, key, "a new request must skip cooling keys")
					service.FinishChannelKeyProbe(ctx, false)
					redisServer.FastForward(25 * time.Hour)
					assert.Empty(t, service.LoadMultiKeyCooldowns(channel.Id, "KEY_A"))
				} else {
					assert.Empty(t, service.LoadMultiKeyCooldowns(channel.Id, "KEY_A"), "channel failures must not cool an individual key")
				}
				loaded, err := model.GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, common.ChannelStatusEnabled, loaded.Status, fmt.Sprint(got))
				assert.Empty(t, loaded.ChannelInfo.MultiKeyStatusList, "temporary failures must not permanently disable the channel or keys")
			})
		}
	}
}

// legacyRetryTransportFunc adapts deterministic transport faults to the real relay fixture.
type legacyRetryTransportFunc func(*http.Request) (*http.Response, error)

// RoundTrip delegates one outgoing request to the fixture.
func (f legacyRetryTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestStreamConnectionFailureBoundary preserves commit protection across transport phases.
func TestStreamConnectionFailureBoundary(t *testing.T) {
	for _, phase := range []string{"connecting", "http proxy", "connected", "reconnecting", "no trace", "payload", "error frame", "ping failed", "cancelled"} {
		t.Run(phase, func(t *testing.T) {
			service.InitHttpClient()
			client := service.GetHttpClient()
			previous := client.Transport
			t.Cleanup(func() { client.Transport = previous })
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			requestCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(requestCtx)
			info := &relaycommon.RelayInfo{IsStream: true, StreamStatus: relaycommon.NewStreamStatus(), ChannelMeta: &relaycommon.ChannelMeta{}}
			oldRanges := operation_setting.AutomaticRetryStatusCodeRanges
			operation_setting.AutomaticRetryStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 400, End: 599}}
			t.Cleanup(func() { operation_setting.AutomaticRetryStatusCodeRanges = oldRanges })
			client.Transport = legacyRetryTransportFunc(func(r *http.Request) (*http.Response, error) {
				trace := httptrace.ContextClientTrace(r.Context())
				if phase != "no trace" && trace != nil && trace.GetConn != nil {
					trace.GetConn(r.URL.Host)
				}
				if (phase == "connected" || phase == "reconnecting") && trace != nil && trace.GotConn != nil {
					trace.GotConn(httptrace.GotConnInfo{})
				}
				if phase == "reconnecting" && trace != nil && trace.GetConn != nil {
					trace.GetConn(r.URL.Host)
				}
				switch phase {
				case "payload":
					info.StreamStatus.MarkClientPayloadCommitted()
				case "error frame":
					info.StreamStatus.TryMarkErrorFrameWritten()
				case "ping failed":
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPingFail, errors.New("client write failed"))
				case "cancelled":
					cancel()
				}
				return nil, errors.New("transport fixture failure")
			})
			if phase == "http proxy" {
				transport := &http.Transport{
					Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: "proxy.invalid:8080"}),
					DialContext: func(context.Context, string, string) (net.Conn, error) {
						return nil, &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
					},
				}
				t.Cleanup(transport.CloseIdleConnections)
				client.Transport = transport
			}
			req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, "http://upstream.invalid", strings.NewReader("fixture"))
			require.NoError(t, err)
			_, err = channel.DoRequest(ctx, req, info)
			require.Error(t, err)
			apiErr := types.NewError(err, types.ErrorCodeDoRequestFailed)
			assert.Equal(t, "upstream error: do request failed", apiErr.Error())
			decision := decideHTTPRelayRetry(ctx, info, apiErr, 2)
			if phase == "connecting" || phase == "http proxy" {
				assert.Equal(t, "retry", decision.Action)
			} else if phase == "cancelled" {
				assert.Error(t, ctx.Request.Context().Err())
			} else {
				assert.Equal(t, "stop", decision.Action)
			}
			if phase == "connecting" || phase == "http proxy" {
				info.StreamStatus.PrepareRetryAttempt()
				assert.Equal(t, relaycommon.StreamRetryCommitPolicyHTTP, info.StreamStatus.RetryCommitPolicy())
			}
		})
	}
}
