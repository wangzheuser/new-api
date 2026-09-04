package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

type MultiKeyFailureAction string

const (
	MultiKeyFailureNone       MultiKeyFailureAction = "none"
	MultiKeyFailureTemporary  MultiKeyFailureAction = "temporary"
	MultiKeyFailurePersistent MultiKeyFailureAction = "persistent"
)

// MultiKeyFingerprint returns a stable identifier without retaining the credential itself.
func MultiKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// ClassifyMultiKeyFailure applies the effective channel policy to one real upstream response.
func ClassifyMultiKeyFailure(channel *model.Channel, err *types.NewAPIError) (MultiKeyFailureAction, int) {
	if channel == nil || err == nil || !channel.ChannelInfo.IsMultiKey || !common.AutomaticDisableChannelEnabled || !channel.GetAutoBan() {
		return MultiKeyFailureNone, 0
	}
	statusCode, ok := err.GetUpstreamStatusCode()
	if !ok {
		return MultiKeyFailureNone, 0
	}
	setting := effectiveMultiKeyAutoDisableConfig(channel)
	if operation_setting.MatchMultiKeyStatusCode(setting.PersistentStatusCodes, statusCode) {
		return MultiKeyFailurePersistent, statusCode
	}
	if operation_setting.MatchMultiKeyStatusCode(setting.TemporaryStatusCodes, statusCode) {
		return MultiKeyFailureTemporary, statusCode
	}
	return MultiKeyFailureNone, statusCode
}

// HandleMultiKeyFailure records one classified key failure and reports whether generic channel handling must stop.
func HandleMultiKeyFailure(channel *model.Channel, keyIndex int, usingKey string, err *types.NewAPIError) (MultiKeyFailureAction, bool) {
	action, statusCode := ClassifyMultiKeyFailure(channel, err)
	if action == MultiKeyFailureNone {
		return action, false
	}
	keys := channel.GetKeys()
	if usingKey == "" || keyIndex < 0 || keyIndex >= len(keys) || keys[keyIndex] != usingKey {
		common.SysLog(fmt.Sprintf("skip stale multi-key failure update: channel_id=%d, key_index=%d", channel.Id, keyIndex))
		return action, true
	}

	reasonMessage := strings.ReplaceAll(err.MaskSensitiveError(), usingKey, "***")
	reason := fmt.Sprintf("status_code=%d", statusCode)
	if reasonMessage != "" {
		reason = fmt.Sprintf("status_code=%d, %s", statusCode, reasonMessage)
	}
	switch action {
	case MultiKeyFailurePersistent:
		ClearMultiKeyTemporaryDisable(channel.Id, usingKey)
		if model.UpdateChannelStatus(channel.Id, usingKey, common.ChannelStatusAutoDisabled, reason) {
			subject := fmt.Sprintf("通道「%s」（#%d）的密钥 #%d 已自动禁用", channel.Name, channel.Id, keyIndex+1)
			content := fmt.Sprintf("通道「%s」（#%d）的密钥 #%d 已自动禁用，原因：%s", channel.Name, channel.Id, keyIndex+1, reason)
			NotifyRootUser(fmt.Sprintf("%s_%d_key_%d", dto.NotifyTypeChannelUpdate, channel.Id, keyIndex), subject, content)
			if updated, loadErr := model.CacheGetChannel(channel.Id); loadErr == nil && updated != nil && updated.Status != common.ChannelStatusEnabled {
				channelSubject := fmt.Sprintf("通道「%s」（#%d）的所有密钥均不可用", channel.Name, channel.Id)
				channelContent := fmt.Sprintf("通道「%s」（#%d）已因所有密钥不可用而停止参与路由。", channel.Name, channel.Id)
				NotifyRootUser(fmt.Sprintf("%s_%d_key_pool", dto.NotifyTypeChannelUpdate, channel.Id), channelSubject, channelContent)
			}
		}
	case MultiKeyFailureTemporary:
		setting := effectiveMultiKeyAutoDisableConfig(channel)
		if common.RedisEnabled && common.RDB != nil {
			info := dto.MultiKeyTemporaryDisableInfo{
				DisabledUntil: time.Now().Add(time.Duration(setting.TemporaryDisableMinutes) * time.Minute).Unix(),
				StatusCode:    statusCode,
				Reason:        reason,
			}
			value, marshalErr := common.Marshal(info)
			if marshalErr != nil {
				common.SysLog(fmt.Sprintf("failed to encode multi-key temporary disable: channel_id=%d, error=%v", channel.Id, marshalErr))
				break
			}
			if setErr := common.RDB.Set(
				context.Background(),
				multiKeyTemporaryDisableKey(channel.Id, usingKey),
				value,
				time.Duration(setting.TemporaryDisableMinutes)*time.Minute,
			).Err(); setErr != nil {
				logChannelAutoDisableRedisError(setErr)
			}
		}
	}
	refreshMultiKeyPoolBlock(channel.Id)
	return action, true
}

// SelectNextEnabledChannelKey skips persistent, temporary, and request-local key exclusions.
func SelectNextEnabledChannelKey(channel *model.Channel, excludedFingerprints map[string]struct{}) (string, int, *types.NewAPIError) {
	if channel == nil {
		return "", 0, types.NewError(fmt.Errorf("channel is nil"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if !channel.ChannelInfo.IsMultiKey || !common.AutomaticDisableChannelEnabled || !channel.GetAutoBan() {
		return channel.GetNextEnabledKey()
	}
	keys := channel.GetKeys()
	excludedIndexes := make(map[int]struct{})
	for index, key := range keys {
		if _, excluded := excludedFingerprints[MultiKeyFingerprint(key)]; excluded {
			excludedIndexes[index] = struct{}{}
		}
	}
	for index := range LoadMultiKeyTemporaryDisableInfo(channel) {
		excludedIndexes[index] = struct{}{}
	}
	return channel.GetNextEnabledKeyExcluding(excludedIndexes)
}

// LoadMultiKeyTemporaryDisableInfo returns active cooldowns keyed by current credential index.
func LoadMultiKeyTemporaryDisableInfo(channel *model.Channel) map[int]dto.MultiKeyTemporaryDisableInfo {
	result := make(map[int]dto.MultiKeyTemporaryDisableInfo)
	if channel == nil || !channel.ChannelInfo.IsMultiKey || !common.AutomaticDisableChannelEnabled || !channel.GetAutoBan() || !common.RedisEnabled || common.RDB == nil {
		return result
	}
	keys := channel.GetKeys()
	if len(keys) == 0 {
		return result
	}
	redisKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		redisKeys = append(redisKeys, multiKeyTemporaryDisableKey(channel.Id, key))
	}
	values, err := common.RDB.MGet(context.Background(), redisKeys...).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return result
	}
	for index, value := range values {
		if value == nil {
			continue
		}
		var raw string
		switch typed := value.(type) {
		case string:
			raw = typed
		case []byte:
			raw = string(typed)
		default:
			continue
		}
		var info dto.MultiKeyTemporaryDisableInfo
		if unmarshalErr := common.UnmarshalJsonStr(raw, &info); unmarshalErr == nil && info.DisabledUntil > time.Now().Unix() {
			result[index] = info
		}
	}
	return result
}

// ClearMultiKeyTemporaryDisable removes one key cooldown and releases the pool for reevaluation.
func ClearMultiKeyTemporaryDisable(channelId int, key string) bool {
	if channelId <= 0 || key == "" || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	removed, err := common.RDB.Del(context.Background(), multiKeyTemporaryDisableKey(channelId, key), multiKeyPoolBlockedKey(channelId)).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return false
	}
	return removed > 0
}

// ClearAllMultiKeyTemporaryDisable removes all cooldown state for one channel.
func ClearAllMultiKeyTemporaryDisable(channelId int) bool {
	if channelId <= 0 || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	ctx := context.Background()
	pattern := fmt.Sprintf("newapi:multi-key-disable:{%d}:*", channelId)
	var cursor uint64
	removed := int64(0)
	for {
		keys, nextCursor, err := common.RDB.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			logChannelAutoDisableRedisError(err)
			return removed > 0
		}
		if len(keys) > 0 {
			count, deleteErr := common.RDB.Del(ctx, keys...).Result()
			if deleteErr != nil {
				logChannelAutoDisableRedisError(deleteErr)
				return removed > 0
			}
			removed += count
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return removed > 0
}

// IsMultiKeyPoolTemporarilyDisabled reports whether every usable key is cooling down.
func IsMultiKeyPoolTemporarilyDisabled(channelId int) bool {
	if channelId <= 0 || !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	exists, err := common.RDB.Exists(context.Background(), multiKeyPoolBlockedKey(channelId)).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return false
	}
	return exists > 0
}

func effectiveMultiKeyAutoDisableConfig(channel *model.Channel) operation_setting.MultiKeyAutoDisableSetting {
	setting := operation_setting.GetMultiKeyAutoDisableSetting()
	if channel == nil {
		return setting
	}
	override := channel.GetOtherSettings().MultiKeyAutoDisableOverride
	if override == nil {
		return setting
	}
	normalized, err := operation_setting.ValidateMultiKeyAutoDisableSetting(
		override.TemporaryStatusCodes,
		override.PersistentStatusCodes,
		override.TemporaryDisableMinutes,
	)
	if err != nil {
		return setting
	}
	return normalized
}

func refreshMultiKeyPoolBlock(channelId int) {
	if channelId <= 0 || !common.RedisEnabled || common.RDB == nil {
		return
	}
	channel, err := model.CacheGetChannel(channelId)
	if err != nil || channel == nil || !channel.ChannelInfo.IsMultiKey {
		return
	}
	temporary := LoadMultiKeyTemporaryDisableInfo(channel)
	keys := channel.GetKeys()
	earliest := int64(0)
	hasUsableKey := false
	pollingLock := model.GetChannelPollingLock(channelId)
	pollingLock.Lock()
	for index := range keys {
		status := common.ChannelStatusEnabled
		if storedStatus, exists := channel.ChannelInfo.MultiKeyStatusList[index]; exists {
			status = storedStatus
		}
		if status != common.ChannelStatusEnabled {
			continue
		}
		info, cooling := temporary[index]
		if !cooling {
			hasUsableKey = true
			break
		}
		if earliest == 0 || info.DisabledUntil < earliest {
			earliest = info.DisabledUntil
		}
	}
	pollingLock.Unlock()
	ctx := context.Background()
	if hasUsableKey || earliest == 0 {
		if deleteErr := common.RDB.Del(ctx, multiKeyPoolBlockedKey(channelId)).Err(); deleteErr != nil {
			logChannelAutoDisableRedisError(deleteErr)
		}
		return
	}
	ttl := time.Until(time.Unix(earliest, 0)) + time.Second
	if ttl <= 0 {
		return
	}
	created, setErr := common.RDB.SetNX(ctx, multiKeyPoolBlockedKey(channelId), earliest, ttl).Result()
	if setErr != nil {
		logChannelAutoDisableRedisError(setErr)
		return
	}
	if created {
		subject := fmt.Sprintf("通道「%s」（#%d）的可用密钥均在冷却", channel.Name, channel.Id)
		content := fmt.Sprintf("通道「%s」（#%d）的可用密钥均在冷却，将于 %s 后重新参与路由。", channel.Name, channel.Id, time.Unix(earliest, 0).Format("2006-01-02 15:04:05"))
		NotifyRootUser(fmt.Sprintf("%s_%d_key_pool", dto.NotifyTypeChannelUpdate, channel.Id), subject, content)
	}
}

func multiKeyTemporaryDisableKey(channelId int, key string) string {
	return fmt.Sprintf("newapi:multi-key-disable:{%d}:key:%s", channelId, MultiKeyFingerprint(key))
}

func multiKeyPoolBlockedKey(channelId int) string {
	return fmt.Sprintf("newapi:multi-key-disable:{%d}:pool-blocked", channelId)
}
