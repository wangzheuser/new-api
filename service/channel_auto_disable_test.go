package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemporaryAutoDisableThresholdAndExpiry(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(server.Close)

	previousClient := common.RDB
	previousRedisEnabled := common.RedisEnabled
	previousAutoDisableEnabled := common.AutomaticDisableChannelEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled = true
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RDB = previousClient
		common.RedisEnabled = previousRedisEnabled
		common.AutomaticDisableChannelEnabled = previousAutoDisableEnabled
	})

	setting := effectiveChannelAutoDisableSetting{
		StatusCodes:      "400-599",
		WindowMinutes:    10,
		MinRequests:      30,
		ErrorRatePercent: 80,
		DisableMinutes:   10,
	}
	keys := channelAutoDisableWindowKeys(42, setting, time.Now())
	run := func(isError bool) int64 {
		errorFlag := 0
		if isError {
			errorFlag = 1
		}
		result, runErr := recordChannelUpstreamResponseScript.Run(
			context.Background(),
			common.RDB,
			keys,
			errorFlag,
			(setting.WindowMinutes+2)*60,
			setting.MinRequests,
			setting.ErrorRatePercent,
			setting.DisableMinutes*60,
			time.Now().Unix(),
			setting.WindowMinutes,
			setting.StatusCodes,
		).Result()
		require.NoError(t, runErr)
		values, ok := result.([]interface{})
		require.True(t, ok)
		return redisResultInt64(values[0])
	}

	for range 6 {
		assert.Equal(t, int64(0), run(false))
	}
	for range 23 {
		assert.Equal(t, int64(0), run(true))
	}
	assert.False(t, IsChannelTemporarilyDisabled(42))
	assert.Equal(t, int64(1), run(true))
	assert.True(t, IsChannelTemporarilyDisabled(42))

	info := LoadChannelTemporaryAutoDisable(42)
	require.NotNil(t, info)
	assert.Equal(t, int64(30), info.Requests)
	assert.Equal(t, int64(24), info.Errors)
	assert.Equal(t, float64(80), info.ErrorRate)

	server.FastForward(10 * time.Minute)
	assert.False(t, IsChannelTemporarilyDisabled(42))
}

func TestEffectiveChannelAutoDisableOverride(t *testing.T) {
	channel := &model.Channel{
		OtherSettings: `{"auto_disable_override":{"window_minutes":5,"min_requests":20,"error_rate_percent":70,"disable_minutes":30}}`,
	}

	setting := effectiveChannelAutoDisableConfig(channel)
	assert.Equal(t, 5, setting.WindowMinutes)
	assert.Equal(t, 20, setting.MinRequests)
	assert.Equal(t, 70, setting.ErrorRatePercent)
	assert.Equal(t, 30, setting.DisableMinutes)
}
