package controller

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
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
		for _, scenario := range []string{"recover", "budget exhausted", "protocol event"} {
			t.Run(string(format)+"/"+scenario, func(t *testing.T) {
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
				var mu sync.Mutex
				var attempts []string
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					key := r.Header.Get("X-Api-Key")
					mu.Lock()
					attempts = append(attempts, key)
					mu.Unlock()
					if scenario == "budget exhausted" || (scenario == "recover" && key == "KEY_A") {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = io.WriteString(w, `{"error":{"message":"upstream failed","type":"server_error"}}`)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"MODEL_X\",\"content\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n")
					if scenario == "protocol event" {
						_, _ = io.WriteString(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"message\":\"stream failed\"}}\n\n")
						return
					}
					_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"fixture-ok\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
				}))
				defer upstream.Close()
				channel := &model.Channel{
					Type: constant.ChannelTypeAnthropic, Name: "legacy-fixture", Key: "KEY_A\nKEY_B\nKEY_C\nKEY_D", Status: common.ChannelStatusEnabled,
					AutoBan: common.GetPointer(1), BaseURL: &upstream.URL, Group: "default", Models: "MODEL_X",
					ChannelInfo:   model.ChannelInfo{IsMultiKey: true, MultiKeySize: 4, MultiKeyMode: constant.MultiKeyModePolling},
					OtherSettings: `{"multi_key_auto_disable_override":{"temporary_status_codes":"429,500-503","persistent_status_codes":"401","temporary_disable_minutes":1}}`,
				}
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "MODEL_X", ChannelId: channel.Id, Enabled: true}).Error)
				path := "/v1/messages"
				if format == types.RelayFormatOpenAI {
					path = "/v1/chat/completions"
				}
				router := gin.New()
				router.POST(path, func(c *gin.Context) {
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
					Relay(c, format)
				})
				// Wait for the handler before inspecting stream output and settled consumption.
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"MODEL_X","messages":[{"role":"user","content":"hello"}],"max_tokens":16,"stream":true}`))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(response, request)
				body := response.Body.String()
				mu.Lock()
				got := append([]string(nil), attempts...)
				mu.Unlock()
				expected := map[string]int{"recover": 2, "budget exhausted": 3, "protocol event": 1}[scenario]
				require.Len(t, got, expected, body)
				assert.Equal(t, "KEY_A", got[0])
				for i := 1; i < len(got); i++ {
					assert.NotContains(t, got[:i], got[i], "a failed key must not be retried")
				}
				var logs []model.Log
				require.NoError(t, db.Where("type = ?", model.LogTypeConsume).Find(&logs).Error)
				var user model.User
				var token model.Token
				require.NoError(t, db.First(&user, 1).Error)
				require.NoError(t, db.First(&token, 1).Error)
				if scenario == "recover" {
					assert.Equal(t, 1, strings.Count(body, "fixture-ok"), body)
					assert.NotContains(t, body, `"type":"error"`)
					require.Len(t, logs, 1)
					assert.Positive(t, logs[0].Quota)
					assert.Equal(t, initialQuota-logs[0].Quota, user.Quota)
					assert.Equal(t, user.Quota, token.RemainQuota)
					if format == types.RelayFormatClaude {
						assert.Equal(t, 1, strings.Count(body, "event: message_stop"))
					} else {
						assert.Equal(t, 1, strings.Count(body, "[DONE]"))
					}
					assert.Len(t, service.LoadMultiKeyTemporaryDisableInfo(channel), 1)
					key, _, apiErr := service.SelectNextEnabledChannelKey(channel, nil)
					require.Nil(t, apiErr)
					assert.NotEqual(t, "KEY_A", key, "a new request must skip the cooling key")
					// Cooldown history is retained beyond its active interval for backoff and probes.
					redisServer.FastForward(25 * time.Hour)
					assert.Empty(t, service.LoadMultiKeyTemporaryDisableInfo(channel))
				} else {
					assert.Len(t, logs, 0)
					// Refunds run asynchronously; verify the actual wallet and token balances.
					require.Eventually(t, func() bool {
						return db.First(&user, 1).Error == nil && db.First(&token, 1).Error == nil &&
							user.Quota == initialQuota && token.RemainQuota == initialQuota
					}, time.Second, time.Millisecond)
					assert.Equal(t, 1, strings.Count(body, `"error":`), body)
					assert.NotContains(t, body, "event: message_stop")
					assert.NotContains(t, body, "[DONE]")
					if scenario == "budget exhausted" {
						assert.Len(t, service.LoadMultiKeyTemporaryDisableInfo(channel), 3)
					}
				}
				loaded, err := model.GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, common.ChannelStatusEnabled, loaded.Status, fmt.Sprint(got))
				assert.Empty(t, loaded.ChannelInfo.MultiKeyStatusList, "temporary failures must not permanently disable the channel or keys")
			})
		}
	}
}
