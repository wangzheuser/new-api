package service

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"

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

	channel := &model.Channel{Id: 42, Name: "rolling", AutoBan: common.GetPointer(1), OtherSettings: `{"auto_disable_override":{"sample_size":5,"minimum_sample_size":3,"error_rate_percent":80,"disable_minutes":10}}`}
	for range 2 {
		recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	}
	assert.False(t, IsChannelTemporarilyDisabled(42))
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	assert.True(t, IsChannelTemporarilyDisabled(42))

	info := LoadChannelTemporaryAutoDisable(42)
	require.NotNil(t, info)
	assert.Equal(t, 5, info.SampleSize)
	assert.Equal(t, 3, info.MinimumSampleSize)
	assert.Equal(t, int64(3), info.Requests)
	assert.Equal(t, int64(3), info.Errors)
	assert.Equal(t, float64(100), info.ErrorRate)
	assert.Equal(t, http.StatusServiceUnavailable, info.StatusCode)

	server.FastForward(10 * time.Minute)
	assert.False(t, IsChannelTemporarilyDisabled(42))
}

func TestEffectiveChannelAutoDisableOverride(t *testing.T) {
	channel := &model.Channel{
		OtherSettings: `{"auto_disable_override":{"sample_size":5,"minimum_sample_size":3,"error_rate_percent":70,"disable_minutes":30}}`,
	}

	setting := effectiveChannelAutoDisableConfig(channel)
	assert.Equal(t, 5, setting.SampleSize)
	assert.Equal(t, 3, setting.MinimumSampleSize)
	assert.Equal(t, 70, setting.ErrorRatePercent)
	assert.Equal(t, 30, setting.DisableMinutes)
}

func TestRollingHealthSamplesEvictOldestAndClearAfterBlock(t *testing.T) {
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

	channel := &model.Channel{Id: 43, Name: "rolling", AutoBan: common.GetPointer(1), OtherSettings: `{"auto_disable_override":{"sample_size":3,"minimum_sample_size":3,"error_rate_percent":80,"disable_minutes":10}}`}
	// Two errors are below the minimum sample count.
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	assert.False(t, IsChannelTemporarilyDisabled(channel.Id))

	// A success keeps 2/3 below the configured error-rate threshold.
	recordChannelUpstreamResponse(channel, http.StatusOK, false, "", "")
	assert.False(t, IsChannelTemporarilyDisabled(channel.Id))

	// The oldest successes are evicted, so the latest three errors trigger at 3/3.
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	require.True(t, IsChannelTemporarilyDisabled(channel.Id))
	assert.NotEmpty(t, LoadChannelTemporaryAutoDisable(channel.Id))
	for _, key := range server.Keys() {
		assert.NotContains(t, key, ":samples")
		assert.NotContains(t, key, ":counts")
	}

	assert.True(t, ClearChannelTemporaryAutoDisable(channel.Id))
	recordChannelUpstreamResponse(channel, http.StatusOK, false, "", "")
	assert.False(t, IsChannelTemporarilyDisabled(channel.Id))
}

func TestRollingHealthConfigurationChangeDoesNotReuseSamples(t *testing.T) {
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

	channel := &model.Channel{Id: 44, Name: "config-change", AutoBan: common.GetPointer(1), OtherSettings: `{"auto_disable_override":{"sample_size":3,"minimum_sample_size":3,"error_rate_percent":80,"disable_minutes":10}}`}
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	channel.OtherSettings = `{"auto_disable_override":{"sample_size":4,"minimum_sample_size":3,"error_rate_percent":80,"disable_minutes":10}}`
	recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "", "")
	assert.False(t, IsChannelTemporarilyDisabled(channel.Id), "samples written under the old policy must not satisfy the new policy")
}

func TestChannelHealthErrorClassification(t *testing.T) {
	previousAutoDisableEnabled := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previousAutoDisableEnabled })
	for _, message := range []string{
		"5-hour usage limit reached",
		"monthly usage limit reached",
		"concurrent request limit reached",
		"usage limit exceeded",
		"You exceeded your current quota",
		"quota exceeded",
		"Your credit balance is too low",
	} {
		err := types.NewOpenAIError(errors.New(message), types.ErrorCodeBadResponseStatusCode, http.StatusForbidden, types.ErrOptionWithUpstreamStatusCode(http.StatusForbidden))
		assert.True(t, isTemporaryQuotaError(err, http.StatusForbidden), message)
	}
	assert.True(t, isTemporaryQuotaError(types.NewOpenAIError(errors.New("rate limited"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), http.StatusTooManyRequests))
	authError := types.NewOpenAIError(errors.New("usage limit"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized, types.ErrOptionWithUpstreamStatusCode(http.StatusUnauthorized))
	assert.False(t, isTemporaryQuotaError(authError, http.StatusUnauthorized))
	assert.True(t, ShouldDisableChannel(authError))
	assert.True(t, isIgnoredChannelHealthError("context_length_exceeded"))
	assert.True(t, isIgnoredChannelHealthError("role 'developer' is not allowed"))
	assert.True(t, isIgnoredChannelHealthError("tool_choice 'specified' is incompatible"))

	for _, statusCode := range []int{http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway} {
		err := types.NewOpenAIError(errors.New("upstream failure"), types.ErrorCodeBadResponseStatusCode, statusCode, types.ErrOptionWithUpstreamStatusCode(statusCode))
		assert.False(t, ShouldDisableChannel(err), statusCode)
	}
	clientError := types.NewOpenAIError(errors.New("context_length_exceeded"), types.ErrorCodeBadResponseStatusCode, http.StatusBadRequest, types.ErrOptionWithUpstreamStatusCode(http.StatusBadRequest))
	assert.False(t, ShouldDisableChannel(clientError))
	timeoutError := types.NewError(errors.New("context deadline exceeded"), types.ErrorCodeDoRequestFailed)
	assert.False(t, ShouldDisableChannel(timeoutError))
	streamTimeout := types.NewOpenAIError(errors.New("stream ended abnormally: timeout"), types.ErrorCodeBadResponse, http.StatusGatewayTimeout)
	assert.True(t, isRealUpstreamTimeout(streamTimeout))
}

func TestMultiKeyRollingSamplesAreIsolatedByKeyAndModel(t *testing.T) {
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

	channel := &model.Channel{
		Id:            45,
		Name:          "multi-key-rolling",
		Key:           "KEY_A\nKEY_B",
		AutoBan:       common.GetPointer(1),
		OtherSettings: `{"auto_disable_override":{"sample_size":3,"minimum_sample_size":3,"error_rate_percent":80,"disable_minutes":10}}`,
		ChannelInfo:   model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2},
	}
	for range 3 {
		recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "KEY_A", "MODEL_A")
	}
	assert.False(t, IsMultiKeyModelPoolBlocked(channel, "MODEL_A"), "the other key remains available")
	assert.False(t, IsMultiKeyModelPoolBlocked(channel, "MODEL_B"), "a different model remains isolated")
	require.Len(t, LoadMultiKeyCooldowns(channel.Id, "KEY_A"), 1)

	for range 3 {
		recordChannelUpstreamResponse(channel, http.StatusServiceUnavailable, true, "KEY_B", "MODEL_A")
	}
	assert.True(t, IsMultiKeyModelPoolBlocked(channel, "MODEL_A"))
	assert.False(t, IsMultiKeyModelPoolBlocked(channel, "MODEL_B"))
}
