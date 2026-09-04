package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupMultiKeyHealthRedis installs an isolated Redis instance and restores globals after the test.
func setupMultiKeyHealthRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server, err := miniredis.Run()
	require.NoError(t, err)
	previousClient := common.RDB
	previousRedisEnabled := common.RedisEnabled
	previousAutoDisableEnabled := common.AutomaticDisableChannelEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		_ = common.RDB.Close()
		server.Close()
		common.RDB = previousClient
		common.RedisEnabled = previousRedisEnabled
		common.AutomaticDisableChannelEnabled = previousAutoDisableEnabled
	})
	return server
}

// createMultiKeyHealthChannel persists a deterministic two-key channel.
func createMultiKeyHealthChannel(t *testing.T, db *gorm.DB, mode constant.MultiKeyMode) *model.Channel {
	t.Helper()
	autoBan := 1
	channel := &model.Channel{
		Type:        constant.ChannelTypeOpenAI,
		Key:         "KEY_A\nKEY_B",
		Status:      common.ChannelStatusEnabled,
		Name:        "multi-key-health",
		Models:      "MODEL_X",
		Group:       "default",
		AutoBan:     &autoBan,
		CreatedTime: common.GetTimestamp(),
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyMode:       mode,
			MultiKeyStatusList: make(map[int]int),
		},
	}
	require.NoError(t, db.Create(channel).Error)
	return channel
}

// upstreamStatusError builds an error carrying the unmodified upstream status code.
func upstreamStatusError(statusCode int, message string) *types.NewAPIError {
	return types.NewOpenAIError(
		errors.New(message),
		types.ErrorCodeBadResponseStatusCode,
		statusCode,
		types.ErrOptionWithUpstreamStatusCode(statusCode),
	)
}

func TestClassifyMultiKeyFailureUsesOnlyRealUpstreamStatus(t *testing.T) {
	autoBan := 1
	channel := &model.Channel{
		AutoBan:     &autoBan,
		ChannelInfo: model.ChannelInfo{IsMultiKey: true},
	}
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })

	action, statusCode := ClassifyMultiKeyFailure(channel, upstreamStatusError(http.StatusTooManyRequests, "limited"))
	assert.Equal(t, MultiKeyFailureTemporary, action)
	assert.Equal(t, http.StatusTooManyRequests, statusCode)

	action, statusCode = ClassifyMultiKeyFailure(channel, upstreamStatusError(http.StatusUnauthorized, "invalid key"))
	assert.Equal(t, MultiKeyFailurePersistent, action)
	assert.Equal(t, http.StatusUnauthorized, statusCode)

	local429 := types.NewOpenAIError(errors.New("local limit"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
	action, _ = ClassifyMultiKeyFailure(channel, local429)
	assert.Equal(t, MultiKeyFailureNone, action)

	mapped429 := types.NewOpenAIError(
		errors.New("mapped"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusTooManyRequests,
		types.ErrOptionWithUpstreamStatusCode(http.StatusInternalServerError),
	)
	action, _ = ClassifyMultiKeyFailure(channel, mapped429)
	assert.Equal(t, MultiKeyFailureNone, action)
}

func TestTemporaryMultiKeyDisableSkipsKeyAndExpires(t *testing.T) {
	server := setupMultiKeyHealthRedis(t)
	db := setupChannelSelectProtocolTestDB(t)
	channel := createMultiKeyHealthChannel(t, db, constant.MultiKeyModeRandom)

	action, handled := HandleMultiKeyFailure(channel, 0, "KEY_A", upstreamStatusError(http.StatusTooManyRequests, "quota exceeded for KEY_A"))
	assert.Equal(t, MultiKeyFailureTemporary, action)
	assert.True(t, handled)

	temporary := LoadMultiKeyTemporaryDisableInfo(channel)
	require.Contains(t, temporary, 0)
	assert.Equal(t, http.StatusTooManyRequests, temporary[0].StatusCode)
	assert.NotContains(t, temporary[0].Reason, "KEY_A")
	assert.Contains(t, server.Keys(), multiKeyTemporaryDisableKey(channel.Id, "KEY_A"))
	for _, redisKey := range server.Keys() {
		assert.NotContains(t, redisKey, "KEY_A")
	}

	selected, index, selectErr := SelectNextEnabledChannelKey(channel, nil)
	require.Nil(t, selectErr)
	assert.Equal(t, "KEY_B", selected)
	assert.Equal(t, 1, index)
	assert.True(t, ClearMultiKeyTemporaryDisable(channel.Id, "KEY_A"))
	assert.Empty(t, LoadMultiKeyTemporaryDisableInfo(channel))

	_, handled = HandleMultiKeyFailure(channel, 0, "KEY_A", upstreamStatusError(http.StatusTooManyRequests, "quota exceeded"))
	assert.True(t, handled)

	server.FastForward(10 * time.Minute)
	assert.Empty(t, LoadMultiKeyTemporaryDisableInfo(channel))
}

func TestMultiKeyRequestExclusionWorksForPolling(t *testing.T) {
	setupMultiKeyHealthRedis(t)
	db := setupChannelSelectProtocolTestDB(t)
	channel := createMultiKeyHealthChannel(t, db, constant.MultiKeyModePolling)

	selected, index, selectErr := SelectNextEnabledChannelKey(channel, map[string]struct{}{
		MultiKeyFingerprint("KEY_A"): {},
	})

	require.Nil(t, selectErr)
	assert.Equal(t, "KEY_B", selected)
	assert.Equal(t, 1, index)
}

func TestAllCoolingKeysBlockPoolUntilEarliestExpiry(t *testing.T) {
	server := setupMultiKeyHealthRedis(t)
	db := setupChannelSelectProtocolTestDB(t)
	channel := createMultiKeyHealthChannel(t, db, constant.MultiKeyModeRandom)
	limited := upstreamStatusError(http.StatusTooManyRequests, "limited")

	_, handled := HandleMultiKeyFailure(channel, 0, "KEY_A", limited)
	assert.True(t, handled)
	assert.False(t, IsMultiKeyPoolTemporarilyDisabled(channel.Id))
	_, handled = HandleMultiKeyFailure(channel, 1, "KEY_B", limited)
	assert.True(t, handled)
	assert.True(t, IsMultiKeyPoolTemporarilyDisabled(channel.Id))

	server.FastForward(10*time.Minute + 2*time.Second)
	assert.False(t, IsMultiKeyPoolTemporarilyDisabled(channel.Id))
}

func TestChannelSelectionSkipsFullyCoolingMultiKeyPool(t *testing.T) {
	setupMultiKeyHealthRedis(t)
	db := setupChannelSelectProtocolTestDB(t)
	highPriority := createProtocolSelectionChannel(t, db, "cooling-multi-key", 10, constant.EndpointTypeOpenAIResponse)
	available := createProtocolSelectionChannel(t, db, "available", 1, constant.EndpointTypeOpenAIResponse)
	highPriority.Key = "KEY_A\nKEY_B"
	highPriority.ChannelInfo = model.ChannelInfo{
		IsMultiKey:   true,
		MultiKeySize: 2,
		MultiKeyMode: constant.MultiKeyModeRandom,
	}
	require.NoError(t, db.Save(highPriority).Error)
	require.NoError(t, common.RedisSet(multiKeyPoolBlockedKey(highPriority.Id), "1", time.Minute))

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	selected, _, _, err := CacheGetRandomSatisfiedChannelWithRoute(&RetryParam{
		Ctx:         ctx,
		TokenGroup:  "vip",
		ModelName:   "MODEL_X",
		RequestPath: "/v1/responses",
		Retry:       common.GetPointer(0),
	})

	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, available.Id, selected.Id)
}

func TestPersistentMultiKeyDisableKeepsOtherKeyAvailable(t *testing.T) {
	setupMultiKeyHealthRedis(t)
	db := setupChannelSelectProtocolTestDB(t)
	channel := createMultiKeyHealthChannel(t, db, constant.MultiKeyModeRandom)

	action, handled := HandleMultiKeyFailure(channel, 0, "KEY_A", upstreamStatusError(http.StatusUnauthorized, "invalid KEY_A"))
	assert.Equal(t, MultiKeyFailurePersistent, action)
	assert.True(t, handled)

	updated, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, updated.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, common.ChannelStatusEnabled, updated.Status)
	assert.NotContains(t, updated.ChannelInfo.MultiKeyDisabledReason[0], "KEY_A")

	selected, index, selectErr := SelectNextEnabledChannelKey(updated, nil)
	require.Nil(t, selectErr)
	assert.Equal(t, "KEY_B", selected)
	assert.Equal(t, 1, index)
}

func TestTemporaryMultiKeyDisableFailsOpenWithoutRedis(t *testing.T) {
	autoBan := 1
	channel := &model.Channel{
		Id:      99,
		Key:     "KEY_A\nKEY_B",
		AutoBan: &autoBan,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
			MultiKeyMode: constant.MultiKeyModeRandom,
		},
	}
	previousRedisEnabled := common.RedisEnabled
	previousAutoDisableEnabled := common.AutomaticDisableChannelEnabled
	common.RedisEnabled = false
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.AutomaticDisableChannelEnabled = previousAutoDisableEnabled
	})

	action, handled := HandleMultiKeyFailure(channel, 0, "KEY_A", upstreamStatusError(http.StatusTooManyRequests, "limited"))
	assert.Equal(t, MultiKeyFailureTemporary, action)
	assert.True(t, handled)
	assert.Empty(t, LoadMultiKeyTemporaryDisableInfo(channel))

	selected, _, selectErr := SelectNextEnabledChannelKey(channel, nil)
	require.Nil(t, selectErr)
	assert.Contains(t, []string{"KEY_A", "KEY_B"}, selected)
}

func TestEffectiveMultiKeyAutoDisableOverride(t *testing.T) {
	channel := &model.Channel{
		OtherSettings: `{"multi_key_auto_disable_override":{"temporary_status_codes":"408,429","persistent_status_codes":"401,403","temporary_disable_minutes":30}}`,
	}

	setting := effectiveMultiKeyAutoDisableConfig(channel)
	assert.Equal(t, "408,429", setting.TemporaryStatusCodes)
	assert.Equal(t, "401,403", setting.PersistentStatusCodes)
	assert.Equal(t, 30, setting.TemporaryDisableMinutes)
}
