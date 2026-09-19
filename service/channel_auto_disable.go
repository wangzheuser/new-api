package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-redis/redis/v8"
)

const channelAutoDisableRedisErrorLogInterval = time.Minute

var channelAutoDisableLastRedisErrorLog atomic.Int64

var recordChannelUpstreamResponseScript = redis.NewScript(`
local existingBlock = redis.call('GET', KEYS[1])
if existingBlock then
    local ok, block = pcall(cjson.decode, existingBlock)
    if not ok or tonumber(block.disabled_until or '0') > tonumber(ARGV[6]) then
        return {0, 0, 0}
    end
    redis.call('DEL', KEYS[1])
end

local sample = ARGV[1]
local maxSamples = tonumber(ARGV[2])
redis.call('RPUSH', KEYS[2], sample)
redis.call('HINCRBY', KEYS[3], 'sample_count', 1)
if sample == '1' then
    redis.call('HINCRBY', KEYS[3], 'error_count', 1)
end

local count = redis.call('LLEN', KEYS[2])
while count > maxSamples do
    local old = redis.call('LPOP', KEYS[2])
    redis.call('HINCRBY', KEYS[3], 'sample_count', -1)
    if old == '1' then
        redis.call('HINCRBY', KEYS[3], 'error_count', -1)
    end
    count = count - 1
end

local retention = math.max(tonumber(ARGV[5]) + 86400, 3600)
redis.call('EXPIRE', KEYS[2], retention)
redis.call('EXPIRE', KEYS[3], retention)

local total = tonumber(redis.call('HGET', KEYS[3], 'sample_count') or '0')
local errors = tonumber(redis.call('HGET', KEYS[3], 'error_count') or '0')
if total < tonumber(ARGV[3]) or errors * 100 < total * tonumber(ARGV[4]) then
    return {0, total, errors}
end

local disabledUntil = tonumber(ARGV[6]) + tonumber(ARGV[5])
local info = cjson.encode({
    disabled_until = disabledUntil,
    config_fingerprint = ARGV[10],
    key_fingerprint = ARGV[11],
    sample_size = maxSamples,
    minimum_sample_size = tonumber(ARGV[3]),
    requests = total,
    errors = errors,
    error_rate_percent = errors * 100 / total,
    status_codes = ARGV[7],
    scope = ARGV[8],
    model = ARGV[9],
    status_code = tonumber(ARGV[12])
})
local created = redis.call('SET', KEYS[1], info, 'EX', tonumber(ARGV[5]), 'NX')
if not created then
    return {0, total, errors}
end

redis.call('DEL', KEYS[2], KEYS[3])
return {1, total, errors}
`)

type effectiveChannelAutoDisableSetting struct {
	StatusCodes       string
	SampleSize        int
	MinimumSampleSize int
	ErrorRatePercent  int
	DisableMinutes    int
}

// RecordChannelUpstreamResponseAsync records one successful or classified upstream response asynchronously.
func RecordChannelUpstreamResponseAsync(channel *model.Channel, upstreamStatusCode int, identity ...string) {
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
		ChannelInfo:   channel.ChannelInfo,
	}
	key, upstreamModel := "", ""
	if len(identity) > 0 {
		key = identity[0]
	}
	if len(identity) > 1 {
		upstreamModel = identity[1]
	}
	gopool.Go(func() {
		recordChannelUpstreamResponse(snapshot, upstreamStatusCode, false, key, upstreamModel)
	})
}

// RecordChannelUpstreamErrorAsync classifies one upstream error and records only health-relevant outcomes.
func RecordChannelUpstreamErrorAsync(channel *model.Channel, apiErr *types.NewAPIError, usingKey, upstreamModel string) {
	if channel == nil || apiErr == nil || !common.AutomaticDisableChannelEnabled || !channel.GetAutoBan() || !common.RedisEnabled || common.RDB == nil {
		return
	}
	snapshot := &model.Channel{
		Id:            channel.Id,
		Name:          channel.Name,
		AutoBan:       common.GetPointer(1),
		OtherSettings: channel.OtherSettings,
		ChannelInfo:   channel.ChannelInfo,
	}
	gopool.Go(func() {
		recordChannelUpstreamError(snapshot, apiErr, usingKey, upstreamModel)
	})
}

// recordChannelUpstreamError applies the priority order for persistent, temporary and statistical failures.
func recordChannelUpstreamError(channel *model.Channel, apiErr *types.NewAPIError, usingKey, upstreamModel string) {
	statusCode, realUpstream := apiErr.GetUpstreamStatusCode()
	message := strings.ToLower(apiErr.Error())
	if !realUpstream && isRealUpstreamTimeout(apiErr) {
		statusCode = http.StatusGatewayTimeout
		realUpstream = true
	}
	if !realUpstream {
		return
	}
	if statusCode == http.StatusUnauthorized {
		return
	}
	if isIgnoredChannelHealthError(message) {
		return
	}
	if isTemporaryQuotaError(apiErr, statusCode) {
		if !channel.ChannelInfo.IsMultiKey {
			blockSingleKeyChannelTemporarily(channel, apiErr, upstreamModel)
		}
		return
	}
	if !operation_setting.ShouldCountChannelAutoDisableStatusCode(statusCode) {
		return
	}
	recordChannelUpstreamResponse(channel, statusCode, true, usingKey, upstreamModel)
}

// isRealUpstreamTimeout identifies transport timeouts that represent an upstream health failure.
func isRealUpstreamTimeout(apiErr *types.NewAPIError) bool {
	if apiErr == nil {
		return false
	}
	message := strings.ToLower(apiErr.Error())
	if strings.Contains(message, "timeout") || strings.Contains(message, "deadline") {
		return true
	}
	return apiErr.StatusCode == http.StatusGatewayTimeout && apiErr.GetErrorCode() == types.ErrorCodeBadResponse
}

// recordChannelUpstreamResponse appends one result to the scope's rolling sample list.
func recordChannelUpstreamResponse(channel *model.Channel, upstreamStatusCode int, isError bool, usingKey, upstreamModel string) {
	if channel == nil || !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil {
		return
	}
	setting := effectiveChannelAutoDisableConfig(channel)
	now := time.Now()
	scope, modelName := "channel", ""
	scopeLabel := "channel"
	if channel.ChannelInfo.IsMultiKey {
		scope = "key:" + MultiKeyFingerprint(usingKey)
		scopeLabel = "key_model"
		modelName = upstreamModel
		if modelName == "" {
			modelName = "unknown"
		}
	}
	if modelName == "" {
		modelName = "-"
	}
	blockKey, sampleKey, counterKey := channelAutoDisableScopeKeys(channel.Id, scope, modelName, setting)
	errorFlag := 0
	if isError {
		errorFlag = 1
	}
	args := []interface{}{
		errorFlag,
		setting.SampleSize,
		setting.MinimumSampleSize,
		setting.ErrorRatePercent,
		setting.DisableMinutes * 60,
		now.Unix(),
		setting.StatusCodes,
		scopeLabel,
		modelName,
		channelAutoDisableConfigFingerprint(setting),
		func() string {
			if channel.ChannelInfo.IsMultiKey {
				return MultiKeyFingerprint(usingKey)
			}
			return ""
		}(),
		upstreamStatusCode,
	}

	result, err := recordChannelUpstreamResponseScript.Run(context.Background(), common.RDB, []string{blockKey, sampleKey, counterKey}, args...).Result()
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
	if channel.ChannelInfo.IsMultiKey {
		refreshMultiKeyPoolBlock(channel.Id, modelName)
	}
	subject := fmt.Sprintf("通道「%s」（#%d）已被临时禁用", channel.Name, channel.Id)
	scopeText := "渠道"
	if channel.ChannelInfo.IsMultiKey {
		scopeText = fmt.Sprintf("密钥 #%s / 模型 %s", MultiKeyFingerprint(usingKey)[:8], modelName)
	}
	content := fmt.Sprintf(
		"通道「%s」（#%d）的%s在最近 %d 次有效上游响应中有 %d 次错误，错误率 %.1f%%，已临时禁用 %d 分钟，将于 %s 自动恢复。",
		channel.Name,
		channel.Id,
		scopeText,
		requests,
		errorsCount,
		errorRate,
		setting.DisableMinutes,
		disabledUntil.Format("2006-01-02 15:04:05"),
	)
	NotifyRootUser(fmt.Sprintf("%s_%d_temporary", dto.NotifyTypeChannelUpdate, channel.Id), subject, content)
}

// isIgnoredChannelHealthError filters known client-side protocol failures from health samples.
func isIgnoredChannelHealthError(message string) bool {
	return strings.Contains(message, "context_length_exceeded") ||
		(strings.Contains(message, "developer") && strings.Contains(message, "role") && strings.Contains(message, "not allowed")) ||
		(strings.Contains(message, "tool_choice") && strings.Contains(message, "incompatible"))
}

// isTemporaryQuotaError recognizes provider quota and rate-limit failures before generic keyword handling.
func isTemporaryQuotaError(apiErr *types.NewAPIError, statusCode int) bool {
	if statusCode == http.StatusUnauthorized {
		return false
	}
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	message := strings.ToLower(apiErr.Error())
	code := strings.ToLower(fmt.Sprint(apiErr.ToOpenAIError().Code))
	return isTemporaryQuotaMessage(message) ||
		code == "insufficient_quota" ||
		code == "inference_cap_error"
}

// isTemporaryQuotaMessage recognizes provider quota and rate-limit wording before generic disable keywords.
func isTemporaryQuotaMessage(message string) bool {
	return isProviderQuotaMessage(message) || isAccountQuotaMessage(message)
}

// isProviderQuotaMessage recognizes provider-specific rolling limits.
func isProviderQuotaMessage(message string) bool {
	return strings.Contains(message, "5-hour usage limit") ||
		strings.Contains(message, "monthly usage limit") ||
		strings.Contains(message, "concurrent request limit") ||
		strings.Contains(message, "daily free limit reached") ||
		strings.Contains(message, "rate limit") ||
		strings.Contains(message, "rate-limit") ||
		strings.Contains(message, "too many requests") ||
		strings.Contains(message, "throttled")
}

// isAccountQuotaMessage recognizes account and plan quota exhaustion.
func isAccountQuotaMessage(message string) bool {
	return strings.Contains(message, "usage limit") ||
		strings.Contains(message, "quota exceeded") ||
		strings.Contains(message, "exceeded your current quota") ||
		strings.Contains(message, "credit balance is too low") ||
		strings.Contains(message, "insufficient_quota") ||
		strings.Contains(message, "token plan entitlement exhausted") ||
		strings.Contains(message, "workspace allocated quota exceeded")
}

// IsTemporaryQuotaError reports whether an upstream error should be isolated temporarily.
func IsTemporaryQuotaError(apiErr *types.NewAPIError) bool {
	if apiErr == nil {
		return false
	}
	statusCode, _ := apiErr.GetUpstreamStatusCode()
	return isTemporaryQuotaError(apiErr, statusCode)
}

// blockSingleKeyChannelTemporarily creates an immediate quota block for a single-key channel.
func blockSingleKeyChannelTemporarily(channel *model.Channel, apiErr *types.NewAPIError, upstreamModel string) {
	setting := effectiveChannelAutoDisableConfig(channel)
	now := time.Now()
	disabledUntil := now.Add(time.Duration(setting.DisableMinutes) * time.Minute)
	recoverAt := int64(0)
	if retryAt, valid := parseMultiKeyRetryAfter(apiErr.GetUpstreamRetryAfter(), now); valid && retryAt > disabledUntil.Unix() {
		disabledUntil = time.Unix(retryAt, 0)
		recoverAt = retryAt
	}
	statusCode, _ := apiErr.GetUpstreamStatusCode()
	info := dto.TemporaryAutoDisableInfo{
		DisabledUntil:     disabledUntil.Unix(),
		SampleSize:        setting.SampleSize,
		MinimumSampleSize: setting.MinimumSampleSize,
		Requests:          0,
		Errors:            0,
		ErrorRate:         0,
		StatusCodes:       setting.StatusCodes,
		ConfigFingerprint: channelAutoDisableConfigFingerprint(setting),
		Scope:             "channel",
		Model:             upstreamModel,
		StatusCode:        statusCode,
		Reason:            apiErr.MaskSensitiveError(),
	}
	raw, err := common.Marshal(info)
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return
	}
	_, err = common.RDB.Eval(
		context.Background(),
		recordKeyCooldown,
		[]string{channelAutoDisableBlockedKey(channel.Id)},
		raw,
		now.Unix(),
		setting.DisableMinutes*60,
		recoverAt,
		rand.Float64()*0.1,
	).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return
	}
	_, sampleKey, counterKey := channelAutoDisableScopeKeys(channel.Id, "channel", "-", setting)
	if err := common.RDB.Del(context.Background(), sampleKey, counterKey).Err(); err != nil {
		logChannelAutoDisableRedisError(err)
	}
}

// IsChannelTemporarilyDisabled reports whether normal relay traffic must skip the channel.
func IsChannelTemporarilyDisabled(channelId int) bool {
	if channelId <= 0 || !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	value, err := common.RDB.Get(context.Background(), channelAutoDisableBlockedKey(channelId)).Result()
	if errors.Is(err, redis.Nil) {
		return false
	}
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return false
	}
	var info dto.TemporaryAutoDisableInfo
	if err := common.UnmarshalJsonStr(value, &info); err != nil {
		logChannelAutoDisableRedisError(err)
		return true
	}
	if info.DisabledUntil <= 0 {
		return true
	}
	return info.DisabledUntil > time.Now().Unix()
}

// IsChannelRoutingBlocked reports whether a channel or its selected key-model scope must be excluded.
func IsChannelRoutingBlocked(channel *model.Channel, modelName string) bool {
	if channel == nil || !channel.GetAutoBan() {
		return false
	}
	healthModel, err := common.ResolveMappedModel(channel.GetModelMapping(), modelName)
	if err != nil {
		return true
	}
	if IsChannelTemporarilyDisabled(channel.Id) {
		return true
	}
	if !channel.ChannelInfo.IsMultiKey {
		return false
	}
	return IsMultiKeyPoolTemporarilyDisabled(channel.Id, healthModel) || IsMultiKeyModelPoolBlocked(channel, healthModel)
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
	pattern := fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:*", channelId)
	var cursor uint64
	removed := int64(0)
	for {
		keys, nextCursor, err := common.RDB.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			logChannelAutoDisableRedisError(err)
			return released > 0
		}
		if len(keys) > 0 {
			count, err := common.RDB.Del(ctx, keys...).Result()
			if err != nil {
				logChannelAutoDisableRedisError(err)
				return released > 0
			}
			removed += count
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return released > 0 || removed > 0
}

// effectiveChannelAutoDisableConfig resolves a complete global or per-channel configuration.
func effectiveChannelAutoDisableConfig(channel *model.Channel) effectiveChannelAutoDisableSetting {
	global := operation_setting.GetChannelAutoDisableSetting()
	setting := effectiveChannelAutoDisableSetting{
		StatusCodes:       global.StatusCodes,
		SampleSize:        global.SampleSize,
		MinimumSampleSize: global.MinimumSampleSize,
		ErrorRatePercent:  global.ErrorRatePercent,
		DisableMinutes:    global.DisableMinutes,
	}
	if channel == nil {
		return setting
	}
	override := channel.GetOtherSettings().AutoDisableOverride
	if override == nil {
		return setting
	}
	if override.SampleSize < operation_setting.ChannelAutoDisableMinSampleSize || override.SampleSize > operation_setting.ChannelAutoDisableMaxSampleSize ||
		override.MinimumSampleSize < operation_setting.ChannelAutoDisableMinMinimumSamples || override.MinimumSampleSize > override.SampleSize ||
		override.ErrorRatePercent < operation_setting.ChannelAutoDisableMinErrorRate || override.ErrorRatePercent > operation_setting.ChannelAutoDisableMaxErrorRate ||
		override.DisableMinutes < operation_setting.ChannelAutoDisableMinDisableMinutes || override.DisableMinutes > operation_setting.ChannelAutoDisableMaxDisableMinutes {
		return setting
	}
	setting.SampleSize = override.SampleSize
	setting.MinimumSampleSize = override.MinimumSampleSize
	setting.ErrorRatePercent = override.ErrorRatePercent
	setting.DisableMinutes = override.DisableMinutes
	return setting
}

// channelAutoDisableScopeKeys builds Redis-cluster-safe keys for one channel or key-model scope.
func channelAutoDisableScopeKeys(channelId int, scope, modelName string, setting effectiveChannelAutoDisableSetting) (string, string, string) {
	configFingerprint := channelAutoDisableConfigFingerprint(setting)
	scopeFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(scope+":"+modelName)))[:16]
	prefix := fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:scope:%s:config:%s", channelId, scopeFingerprint, configFingerprint)
	if strings.HasPrefix(scope, "key:") {
		keyFingerprint := strings.TrimPrefix(scope, "key:")
		modelFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(modelName)))[:16]
		prefix = fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:scope:key:%s:model:%s:config:%s", channelId, keyFingerprint, modelFingerprint, configFingerprint)
	}
	if scope == "channel" {
		return channelAutoDisableBlockedKey(channelId), prefix + ":samples", prefix + ":counts"
	}
	return prefix + ":blocked", prefix + ":samples", prefix + ":counts"
}

// channelAutoDisableConfigFingerprint identifies the complete statistical policy used by a sample bucket.
func channelAutoDisableConfigFingerprint(setting effectiveChannelAutoDisableSetting) string {
	value := fmt.Sprintf("%s|%d|%d|%d|%d", setting.StatusCodes, setting.SampleSize, setting.MinimumSampleSize, setting.ErrorRatePercent, setting.DisableMinutes)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:16]
}

// channelAutoDisableBlockedKey returns the TTL-backed routing exclusion key.
func channelAutoDisableBlockedKey(channelId int) string {
	return fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:blocked", channelId)
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
