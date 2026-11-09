package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelCooldownControllerTest(t *testing.T) (*gorm.DB, *miniredis.Miniredis) {
	t.Helper()
	db := setupPerformanceOptionTest(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.User{}))
	require.NoError(t, db.Create(&model.User{Username: "cooldown-root", Role: common.RoleRootUser}).Error)

	server := miniredis.RunT(t)
	oldClient, oldRedis, oldAutoDisable := common.RDB, common.RedisEnabled, common.AutomaticDisableChannelEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	client := common.RDB
	common.RedisEnabled, common.AutomaticDisableChannelEnabled = true, true
	global := config.GlobalConfig.Get("channel_auto_disable_setting").(*operation_setting.ChannelAutoDisableSetting)
	oldGlobal := *global
	*global = operation_setting.ChannelAutoDisableSetting{
		StatusCodes: "408,500-599", SampleSize: 20, MinimumSampleSize: 10, ErrorRatePercent: 80, DisableMinutes: 1,
	}
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled, common.AutomaticDisableChannelEnabled = oldClient, oldRedis, oldAutoDisable
		*global = oldGlobal
	})
	return db, server
}

func TestInitialChannelPreservesAutoDisableSettings(t *testing.T) {
	db, _ := setupChannelCooldownControllerTest(t)
	for _, multiKey := range []bool{false, true} {
		name := "single-key"
		if multiKey {
			name = "multi-key"
		}
		t.Run(name, func(t *testing.T) {
			channel := &model.Channel{
				Name: name, Type: constant.ChannelTypeOpenAI, Key: "test-key", Status: common.ChannelStatusEnabled,
				AutoBan: common.GetPointer(1), ChannelInfo: model.ChannelInfo{IsMultiKey: multiKey},
				OtherSettings: `{"auto_disable_override":{"sample_size":5,"minimum_sample_size":3,"error_rate_percent":80,"disable_minutes":60}}`,
			}
			require.NoError(t, db.Create(channel).Error)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Set("channel_id", channel.Id)
			c.Set("auto_ban", true)
			common.SetContextKey(c, constant.ContextKeyChannelSnapshot, channel.Snapshot())
			info := &relaycommon.RelayInfo{AttemptModelName: "test-model"}

			selected, apiErr := getChannel(c, info, &service.RetryParam{Retry: common.GetPointer(0)})
			require.Nil(t, apiErr)
			require.NotNil(t, selected)
			assert.Equal(t, channel.OtherSettings, selected.OtherSettings)
			assert.Equal(t, channel.Key, selected.Key)
			assert.Equal(t, multiKey, selected.ChannelInfo.IsMultiKey)

			require.NoError(t, db.Delete(channel).Error)
			fallback, apiErr := getChannel(c, info, &service.RetryParam{Retry: common.GetPointer(0)})
			require.Nil(t, apiErr)
			assert.Equal(t, channel.OtherSettings, fallback.OtherSettings, "a database load failure must retain the distributor snapshot")
		})
	}
}

func TestInitialChannelAutoDisablePolicy(t *testing.T) {
	const override = `{"auto_disable_override":{"sample_size":5,"minimum_sample_size":3,"error_rate_percent":80,"disable_minutes":60}}`
	for _, tc := range []struct {
		name        string
		settings    string
		autoBan     int
		statuses    []int
		samples     int
		minimum     int
		requests    int64
		errors      int64
		minutes     int
		mechanism   string
		fingerprint string
	}{
		{"rolling threshold", override, 1, []int{200, 503, 503, 503, 503}, 5, 3, 5, 4, 60, "rolling_window", "89584c6679b26046"},
		{"minimum samples", override, 1, []int{503, 503, 503}, 5, 3, 3, 3, 60, "rolling_window", "89584c6679b26046"},
		{"quota override", override, 1, []int{429}, 5, 3, 0, 0, 60, "immediate_quota", "89584c6679b26046"},
		{"global fallback", "{}", 1, []int{429}, 20, 10, 0, 0, 1, "immediate_quota", "770e27f74219ff71"},
		{"protection disabled", override, 0, []int{429}, 0, 0, 0, 0, 0, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, server := setupChannelCooldownControllerTest(t)
			channel := &model.Channel{
				Name: tc.name, Type: constant.ChannelTypeOpenAI, Key: "test-key", Status: common.ChannelStatusEnabled,
				AutoBan: &tc.autoBan, OtherSettings: tc.settings,
			}
			require.NoError(t, db.Create(channel).Error)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Set("channel_id", channel.Id)
			c.Set("auto_ban", channel.GetAutoBan())
			info := &relaycommon.RelayInfo{AttemptModelName: "test-model"}
			retry := &service.RetryParam{Retry: common.GetPointer(0)}
			selected, apiErr := getChannel(c, info, retry)
			require.Nil(t, apiErr)
			require.NotNil(t, selected)

			sampleKey := fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:scope:channel:config:%s:samples", channel.Id, tc.fingerprint)
			started := time.Now().Unix()
			for index, status := range tc.statuses {
				if status == http.StatusOK {
					service.RecordChannelUpstreamResponseAsync(selected, status)
				} else {
					upstreamErr := types.NewOpenAIError(errors.New("upstream failure"), types.ErrorCodeBadResponseStatusCode, status, types.ErrOptionWithUpstreamStatusCode(status))
					service.RecordChannelUpstreamErrorAsync(selected, upstreamErr, channel.Key, "test-model")
				}
				if tc.autoBan == 0 {
					assert.False(t, service.IsChannelTemporarilyDisabled(channel.Id))
					assert.Empty(t, server.Keys())
					return
				}
				if index < len(tc.statuses)-1 {
					require.Eventually(t, func() bool {
						return common.RDB.LLen(context.Background(), sampleKey).Val() == int64(index+1)
					}, time.Second, time.Millisecond)
					assert.False(t, service.IsChannelTemporarilyDisabled(channel.Id))
				}
			}
			require.Eventually(t, func() bool {
				return service.IsChannelTemporarilyDisabled(channel.Id)
			}, time.Second, time.Millisecond)
			block := service.LoadChannelTemporaryAutoDisable(channel.Id)
			require.NotNil(t, block)
			assert.Equal(t, tc.samples, block.SampleSize)
			assert.Equal(t, tc.minimum, block.MinimumSampleSize)
			assert.Equal(t, tc.requests, block.Requests)
			assert.Equal(t, tc.errors, block.Errors)
			assert.Equal(t, tc.mechanism, block.Mechanism)
			assert.Equal(t, tc.fingerprint, block.ConfigFingerprint)
			assert.Equal(t, tc.statuses[len(tc.statuses)-1], block.StatusCode)
			assert.GreaterOrEqual(t, block.DisabledUntil, started+int64(tc.minutes*60))
			if tc.mechanism == "rolling_window" {
				assert.Equal(t, float64(tc.errors)*100/float64(tc.requests), block.ErrorRate)
				assert.Equal(t, time.Duration(tc.minutes)*time.Minute, server.TTL(fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:blocked", channel.Id)))
				require.Eventually(t, func() bool {
					for _, key := range server.Keys() {
						if strings.HasPrefix(key, "notify_limit:") {
							return true
						}
					}
					return false
				}, time.Second, time.Millisecond)
			} else {
				assert.LessOrEqual(t, block.DisabledUntil, time.Now().Unix()+int64(tc.minutes*66))
			}

			blocked, apiErr := getChannel(c, info, retry)
			assert.Nil(t, blocked)
			require.NotNil(t, apiErr)
			assert.Equal(t, types.ErrorCodeGetChannelFailed, apiErr.GetErrorCode())
		})
	}
}

// TestMultiKeyCooldownManagement protects two-scope visibility and sensitive writes.
func TestMultiKeyCooldownManagement(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	server := miniredis.RunT(t)
	oldRedis, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	client := common.RDB
	common.RedisEnabled = true
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = oldRedis, oldEnabled
	})
	channel := &model.Channel{Name: "management-fixture", Key: "KEY_A\nKEY_B", Status: 1, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}
	require.NoError(t, db.Create(channel).Error)
	keyName := fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:cooldown:key:%s", channel.Id, service.MultiKeyFingerprint("KEY_A"))
	raw := `{"scope":"key","disabled_until":1,"version":"v1"}`
	require.NoError(t, common.RDB.Set(context.Background(), keyName, raw, time.Hour).Err())
	for _, role := range []int{0, common.RoleRootUser} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("role", role)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key", strings.NewReader(fmt.Sprintf(`{"channel_id":%d,"action":"get_key_status"}`, channel.Id)))
		ManageMultiKeys(c)
		var response struct {
			Success bool                   `json:"success"`
			Data    MultiKeyStatusResponse `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		require.True(t, response.Success, w.Body.String())
		assert.Equal(t, 2, response.Data.EnabledCount)
		assert.Zero(t, response.Data.TemporaryDisabledCount)
		require.Len(t, response.Data.Keys, 2)
		assert.Len(t, response.Data.Keys[0].Cooldowns, 1)
		assert.Equal(t, role == common.RoleRootUser, response.Data.CanManageCooldowns)
		w = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(w)
		c.Set("role", role)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key", strings.NewReader(fmt.Sprintf(`{"channel_id":%d,"key_index":0,"action":"clear_key_cooldown"}`, channel.Id)))
		ManageMultiKeys(c)
		assert.Equal(t, role != common.RoleRootUser, server.Exists(keyName), w.Body.String())
	}
}

// TestMultiKeyTaskRetryCommitBoundary prevents replay after an uncertain upstream task submission.
func TestMultiKeyTaskRetryCommitBoundary(t *testing.T) {
	for _, status := range []int{429, 500} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		assert.False(t, shouldRetryTaskRelay(c, 1, &dto.TaskError{StatusCode: status, SkipRetry: true}, 2))
		c.Writer.WriteHeaderNow()
		assert.False(t, shouldRetryTaskRelay(c, 1, &dto.TaskError{StatusCode: status}, 2))
	}
}
