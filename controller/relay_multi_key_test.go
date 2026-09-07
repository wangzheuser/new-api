package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFirstAttemptRetainsMultiKeyMetadata protects the original first-attempt classification regression.
func TestFirstAttemptRetainsMultiKeyMetadata(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old })
	for _, status := range []int{401, 429} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		channel := &model.Channel{Id: 75, Key: "KEY_A\nKEY_B", AutoBan: common.GetPointer(1), ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, "MODEL_X"))
		firstKey := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
		selected, err := getChannel(c, &relaycommon.RelayInfo{}, &service.RetryParam{})
		require.Nil(t, err)
		require.True(t, selected.ChannelInfo.IsMultiKey)
		assert.Equal(t, firstKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
		upstream := types.NewErrorWithStatusCode(errors.New("upstream failure"), types.ErrorCodeBadResponse, status, types.ErrOptionWithUpstreamStatusCode(status))
		action, _ := service.ClassifyMultiKeyFailure(selected, upstream)
		assert.NotEqual(t, service.MultiKeyFailureNone, action)
	}
}

// TestMultiKeyRelayHTTP exercises first-attempt isolation through the actual HTTP relay.
func TestMultiKeyRelayHTTP(t *testing.T) {
	for _, status := range []int{401, 429} {
		for _, mode := range []constant.MultiKeyMode{constant.MultiKeyModeRandom, constant.MultiKeyModePolling} {
			for _, retries := range []int{0, 2} {
				t.Run(fmt.Sprintf("%d/%s/retries=%d", status, mode, retries), func(t *testing.T) {
					oldDB, oldLogDB := model.DB, model.LOG_DB
					oldRedis, oldRedisEnabled := common.RDB, common.RedisEnabled
					oldRetry, oldAuto, oldMemory := common.RetryTimes, common.AutomaticDisableChannelEnabled, common.MemoryCacheEnabled
					oldRatio := ratio_setting.ModelRatio2JSONString()
					db := setupModelListControllerTestDB(t)
					require.NoError(t, db.AutoMigrate(&model.Log{}))
					require.NoError(t, db.Create(&model.User{Id: 1, Username: "fixture", Role: common.RoleRootUser, Quota: 1000000, Status: common.UserStatusEnabled, Group: "default"}).Error)
					server, err := miniredis.Run()
					require.NoError(t, err)
					common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
					client := common.RDB
					common.RedisEnabled = true
					common.AutomaticDisableChannelEnabled = true
					common.RetryTimes = retries
					common.MemoryCacheEnabled = false
					t.Cleanup(func() {
						_ = client.Close()
						server.Close()
						model.DB = oldDB
						model.LOG_DB = oldLogDB
						common.RDB = oldRedis
						common.RedisEnabled = oldRedisEnabled
						common.RetryTimes = oldRetry
						common.AutomaticDisableChannelEnabled = oldAuto
						common.MemoryCacheEnabled = oldMemory
						require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatio))
					})
					require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"MODEL_X":0}`))
					service.InitHttpClient()
					var mu sync.Mutex
					var attempts []string
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						attempts = append(attempts, r.Header.Get("Authorization"))
						mu.Unlock()
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						_, _ = io.WriteString(w, `{"error":{"message":"limited","type":"rate_limit_error"}}`)
					}))
					defer upstream.Close()
					channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "http-fixture", Key: "KEY_A\nKEY_B", Status: 1, AutoBan: common.GetPointer(1), BaseURL: &upstream.URL, Group: "default", Models: "MODEL_X", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: mode}}
					require.NoError(t, db.Create(channel).Error)
					require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "MODEL_X", ChannelId: channel.Id, Enabled: true}).Error)
					router := gin.New()
					router.POST("/v1/chat/completions", func(c *gin.Context) {
						c.Set("id", 1)
						c.Set("user_id", 1)
						c.Set("group", "default")
						c.Set("token_group", "default")
						c.Set("user_group", "default")
						if apiErr := middleware.SetupContextForSelectedChannel(c, channel, "MODEL_X"); apiErr != nil {
							c.Status(503)
							return
						}
						Relay(c, types.RelayFormatOpenAI)
					})
					gateway := httptest.NewServer(router)
					defer gateway.Close()
					response, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"MODEL_X","messages":[{"role":"user","content":"hello"}],"max_tokens":1}`))
					require.NoError(t, err)
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					assert.NotEqual(t, http.StatusOK, response.StatusCode, string(body))
					mu.Lock()
					got := append([]string(nil), attempts...)
					mu.Unlock()
					expected := 1
					if retries > 0 {
						expected = 2
					}
					require.Len(t, got, expected, string(body))
					if len(got) == 2 {
						assert.NotEqual(t, got[0], got[1])
					}
					if status == 429 {
						assert.Len(t, service.LoadMultiKeyTemporaryDisableInfo(channel), expected, "even zero retries must isolate the first failed key")
					} else {
						loaded, err := model.GetChannelById(channel.Id, true)
						require.NoError(t, err)
						assert.Len(t, loaded.ChannelInfo.MultiKeyStatusList, expected)
					}
				})
			}
		}
	}
}
