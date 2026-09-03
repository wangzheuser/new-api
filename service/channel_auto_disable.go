package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-redis/redis/v8"
)

const channelAutoDisableRedisErrorLogInterval = time.Minute

var channelAutoDisableLastRedisErrorLog atomic.Int64

var recordChannelUpstreamResponseScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
    return {0, 0, 0}
end

redis.call('HINCRBY', KEYS[2], 'total', 1)
if ARGV[1] == '1' then
    redis.call('HINCRBY', KEYS[2], 'errors', 1)
end
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[2]))

if ARGV[1] ~= '1' then
    return {0, 0, 0}
end

local total = 0
local errors = 0
for i = 2, #KEYS do
    total = total + tonumber(redis.call('HGET', KEYS[i], 'total') or '0')
    errors = errors + tonumber(redis.call('HGET', KEYS[i], 'errors') or '0')
end

if total < tonumber(ARGV[3]) or errors * 100 < total * tonumber(ARGV[4]) then
    return {0, total, errors}
end

local disabledUntil = tonumber(ARGV[6]) + tonumber(ARGV[5])
local info = cjson.encode({
    disabled_until = disabledUntil,
    window_minutes = tonumber(ARGV[7]),
    requests = total,
    errors = errors,
    error_rate_percent = errors * 100 / total,
    status_codes = ARGV[8]
})
local created = redis.call('SET', KEYS[1], info, 'EX', tonumber(ARGV[5]), 'NX')
if not created then
    return {0, total, errors}
end

for i = 2, #KEYS do
    redis.call('DEL', KEYS[i])
end
return {1, total, errors}
`)

type effectiveChannelAutoDisableSetting struct {
	StatusCodes      string
	WindowMinutes    int
	MinRequests      int
	ErrorRatePercent int
	DisableMinutes   int
}

// RecordChannelUpstreamResponseAsync records one real upstream HTTP response without delaying the client response.
func RecordChannelUpstreamResponseAsync(channel *model.Channel, upstreamStatusCode int) {
	if channel == nil || channel.Id <= 0 || !common.AutomaticDisableChannelEnabled || !channel.GetAutoBan() || !common.RedisEnabled || common.RDB == nil {
		return
	}
	if upstreamStatusCode < http.StatusContinue || upstreamStatusCode > 599 {
		return
	}

	// Copy the fields read by the worker because cached channel pointers may be refreshed concurrently.
	snapshot := &model.Channel{
		Id:            channel.Id,
		Name:          channel.Name,
		AutoBan:       common.GetPointer(1),
		OtherSettings: channel.OtherSettings,
	}
	gopool.Go(func() {
		recordChannelUpstreamResponse(snapshot, upstreamStatusCode)
	})
}

// recordChannelUpstreamResponse updates the minute buckets and emits one transition notification.
func recordChannelUpstreamResponse(channel *model.Channel, upstreamStatusCode int) {
	if channel == nil || !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil {
		return
	}
	setting := effectiveChannelAutoDisableConfig(channel)
	isError := operation_setting.ShouldCountChannelAutoDisableStatusCode(upstreamStatusCode)
	now := time.Now()
	keys := channelAutoDisableWindowKeys(channel.Id, setting, now)
	errorFlag := 0
	if isError {
		errorFlag = 1
	}
	args := []interface{}{
		errorFlag,
		(setting.WindowMinutes + 2) * 60,
		setting.MinRequests,
		setting.ErrorRatePercent,
		setting.DisableMinutes * 60,
		now.Unix(),
		setting.WindowMinutes,
		setting.StatusCodes,
	}

	result, err := recordChannelUpstreamResponseScript.Run(context.Background(), common.RDB, keys, args...).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 3 || redisResultInt64(values[0]) != 1 {
		return
	}

	requests := redisResultInt64(values[1])
	errorsCount := redisResultInt64(values[2])
	errorRate := float64(errorsCount) * 100 / float64(requests)
	disabledUntil := now.Add(time.Duration(setting.DisableMinutes) * time.Minute)
	subject := fmt.Sprintf("通道「%s」（#%d）已被临时禁用", channel.Name, channel.Id)
	content := fmt.Sprintf(
		"通道「%s」（#%d）在最近 %d 分钟内收到 %d 次上游响应，其中 %d 次命中状态码范围 %s，错误率 %.1f%%，已临时禁用 %d 分钟，将于 %s 自动恢复。",
		channel.Name,
		channel.Id,
		setting.WindowMinutes,
		requests,
		errorsCount,
		setting.StatusCodes,
		errorRate,
		setting.DisableMinutes,
		disabledUntil.Format("2006-01-02 15:04:05"),
	)
	NotifyRootUser(fmt.Sprintf("%s_%d_temporary", dto.NotifyTypeChannelUpdate, channel.Id), subject, content)
}

// IsChannelTemporarilyDisabled reports whether normal relay traffic must skip the channel.
func IsChannelTemporarilyDisabled(channelId int) bool {
	if channelId <= 0 || !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	exists, err := common.RDB.Exists(context.Background(), channelAutoDisableBlockedKey(channelId)).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return false
	}
	return exists > 0
}

// LoadChannelTemporaryAutoDisable returns the current temporary disable details for administrators.
func LoadChannelTemporaryAutoDisable(channelId int) *dto.TemporaryAutoDisableInfo {
	if channelId <= 0 || !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	value, err := common.RDB.Get(context.Background(), channelAutoDisableBlockedKey(channelId)).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return nil
	}
	var info dto.TemporaryAutoDisableInfo
	if err := common.UnmarshalJsonStr(value, &info); err != nil {
		logChannelAutoDisableRedisError(err)
		return nil
	}
	return &info
}

// AttachChannelTemporaryAutoDisable adds Redis-backed state to channel API responses.
func AttachChannelTemporaryAutoDisable(channels []*model.Channel) {
	if !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil || len(channels) == 0 {
		return
	}
	ctx := context.Background()
	pipeline := common.RDB.Pipeline()
	commands := make(map[int]*redis.StringCmd, len(channels))
	for _, channel := range channels {
		if channel == nil || !channel.GetAutoBan() {
			continue
		}
		commands[channel.Id] = pipeline.Get(ctx, channelAutoDisableBlockedKey(channel.Id))
	}
	if len(commands) == 0 {
		return
	}
	if _, err := pipeline.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		logChannelAutoDisableRedisError(err)
		return
	}
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		command := commands[channel.Id]
		if command == nil {
			continue
		}
		value, err := command.Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			logChannelAutoDisableRedisError(err)
			continue
		}
		var info dto.TemporaryAutoDisableInfo
		if err := common.UnmarshalJsonStr(value, &info); err != nil {
			logChannelAutoDisableRedisError(err)
			continue
		}
		channel.TemporaryAutoDisable = &info
	}
}

// ClearChannelTemporaryAutoDisable removes both the active block and all health buckets for a channel.
func ClearChannelTemporaryAutoDisable(channelId int) bool {
	if channelId <= 0 || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	ctx := context.Background()
	released, err := common.RDB.Exists(ctx, channelAutoDisableBlockedKey(channelId)).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return false
	}
	pattern := fmt.Sprintf("newapi:channel-auto-disable:{%d}:*", channelId)
	var cursor uint64
	for {
		keys, nextCursor, err := common.RDB.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			logChannelAutoDisableRedisError(err)
			return released > 0
		}
		if len(keys) > 0 {
			_, err := common.RDB.Del(ctx, keys...).Result()
			if err != nil {
				logChannelAutoDisableRedisError(err)
				return released > 0
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return released > 0
}

// effectiveChannelAutoDisableConfig resolves a complete global or per-channel configuration.
func effectiveChannelAutoDisableConfig(channel *model.Channel) effectiveChannelAutoDisableSetting {
	global := operation_setting.GetChannelAutoDisableSetting()
	setting := effectiveChannelAutoDisableSetting{
		StatusCodes:      global.StatusCodes,
		WindowMinutes:    global.WindowMinutes,
		MinRequests:      global.MinRequests,
		ErrorRatePercent: global.ErrorRatePercent,
		DisableMinutes:   global.DisableMinutes,
	}
	if channel == nil {
		return setting
	}
	override := channel.GetOtherSettings().AutoDisableOverride
	if override == nil {
		return setting
	}
	if override.WindowMinutes < operation_setting.ChannelAutoDisableMinWindowMinutes || override.WindowMinutes > operation_setting.ChannelAutoDisableMaxWindowMinutes ||
		override.MinRequests < operation_setting.ChannelAutoDisableMinRequests || override.MinRequests > operation_setting.ChannelAutoDisableMaxRequests ||
		override.ErrorRatePercent < operation_setting.ChannelAutoDisableMinErrorRate || override.ErrorRatePercent > operation_setting.ChannelAutoDisableMaxErrorRate ||
		override.DisableMinutes < operation_setting.ChannelAutoDisableMinDisableMinutes || override.DisableMinutes > operation_setting.ChannelAutoDisableMaxDisableMinutes {
		return setting
	}
	setting.WindowMinutes = override.WindowMinutes
	setting.MinRequests = override.MinRequests
	setting.ErrorRatePercent = override.ErrorRatePercent
	setting.DisableMinutes = override.DisableMinutes
	return setting
}

// channelAutoDisableWindowKeys builds one Redis-cluster-safe key set for the active minute window.
func channelAutoDisableWindowKeys(channelId int, setting effectiveChannelAutoDisableSetting, now time.Time) []string {
	fingerprintSource := fmt.Sprintf("%s:%d:%d:%d:%d", setting.StatusCodes, setting.WindowMinutes, setting.MinRequests, setting.ErrorRatePercent, setting.DisableMinutes)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprintSource)))[:16]
	minute := now.Unix() / 60
	keys := make([]string, 0, setting.WindowMinutes+1)
	keys = append(keys, channelAutoDisableBlockedKey(channelId))
	for offset := int64(0); offset < int64(setting.WindowMinutes); offset++ {
		keys = append(keys, fmt.Sprintf("newapi:channel-auto-disable:{%d}:bucket:%s:%d", channelId, fingerprint, minute-offset))
	}
	return keys
}

// channelAutoDisableBlockedKey returns the TTL-backed routing exclusion key.
func channelAutoDisableBlockedKey(channelId int) string {
	return fmt.Sprintf("newapi:channel-auto-disable:{%d}:blocked", channelId)
}

// redisResultInt64 normalizes integer results returned by Redis and Lua.
func redisResultInt64(value interface{}) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	case []byte:
		parsed, _ := strconv.ParseInt(string(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

// logChannelAutoDisableRedisError rate-limits fail-open Redis diagnostics.
func logChannelAutoDisableRedisError(err error) {
	now := time.Now().Unix()
	last := channelAutoDisableLastRedisErrorLog.Load()
	if now-last < int64(channelAutoDisableRedisErrorLogInterval/time.Second) || !channelAutoDisableLastRedisErrorLog.CompareAndSwap(last, now) {
		return
	}
	logger.LogWarn(context.Background(), fmt.Sprintf("channel temporary auto-disable Redis operation failed: %v", err))
}
