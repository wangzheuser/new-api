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
	d := DecideMultiKeyFailure(channel, "", err, time.Now())
	return d.Action, d.StatusCode
}

// HandleMultiKeyFailure records one classified key failure and reports whether generic channel handling must stop.
func HandleMultiKeyFailure(channel *model.Channel, keyIndex int, usingKey string, err *types.NewAPIError, upstreamModels ...string) (MultiKeyFailureAction, bool) {
	upstreamModel := ""
	if len(upstreamModels) > 0 {
		upstreamModel = upstreamModels[0]
	}
	decision := DecideMultiKeyFailure(channel, upstreamModel, err, time.Now())
	action, statusCode := decision.Action, decision.StatusCode
	if action == MultiKeyFailureNone {
		return action, false
	}
	// A request must not resurrect a deleted channel or overwrite a later manual decision.
	if model.DB == nil {
		return action, true
	}
	latest, loadErr := model.GetChannelById(channel.Id, true)
	if loadErr != nil || latest == nil || !latest.ChannelInfo.IsMultiKey || latest.Status == common.ChannelStatusManuallyDisabled {
		return action, true
	}
	keys := latest.GetKeys()
	keyIndex = -1
	for index, key := range keys {
		if key == usingKey {
			keyIndex = index
			break
		}
	}
	if usingKey == "" || keyIndex < 0 || keyIndex >= len(keys) || keys[keyIndex] != usingKey {
		common.SysLog(fmt.Sprintf("skip stale multi-key failure update: channel_id=%d, key_index=%d", channel.Id, keyIndex))
		return action, true
	}

	if latest.ChannelInfo.MultiKeyStatusList[keyIndex] == common.ChannelStatusManuallyDisabled {
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
		writeMultiKeyCooldown(channel, usingKey, decision, reason)
	}
	refreshMultiKeyPoolBlock(channel.Id, decision.Model)
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
	redisKeys := make([]string, 0, len(keys)*3)
	for _, key := range keys {
		redisKeys = append(redisKeys, multiKeyTemporaryDisableKey(channel.Id, key))
		redisKeys = append(redisKeys, multiKeyModelDisableKey(channel.Id, key, "unknown"))
		healthBlockKey, _, _ := channelAutoDisableScopeKeys(channel.Id, "key:"+MultiKeyFingerprint(key), "unknown", effectiveChannelAutoDisableConfig(channel))
		redisKeys = append(redisKeys, healthBlockKey)
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
		if strings.Contains(redisKeys[index], ":scope:") {
			var healthInfo dto.TemporaryAutoDisableInfo
			if common.UnmarshalJsonStr(raw, &healthInfo) != nil {
				continue
			}
			info = dto.MultiKeyTemporaryDisableInfo{
				DisabledUntil:     healthInfo.DisabledUntil,
				StatusCode:        healthInfo.StatusCode,
				Reason:            healthInfo.Reason,
				Scope:             healthInfo.Scope,
				Model:             healthInfo.Model,
				KeyFingerprint:    healthInfo.KeyFingerprint,
				Category:          "statistical_health",
				Source:            "rolling_sample",
				SampleSize:        healthInfo.SampleSize,
				MinimumSampleSize: healthInfo.MinimumSampleSize,
				Requests:          healthInfo.Requests,
				Errors:            healthInfo.Errors,
				ErrorRate:         healthInfo.ErrorRate,
			}
		} else if unmarshalErr := common.UnmarshalJsonStr(raw, &info); unmarshalErr != nil {
			continue
		}
		if info.DisabledUntil > time.Now().Unix() {
			keyIndex := index / 3
			if _, exists := result[keyIndex]; !exists {
				result[keyIndex] = info
			}
		}
	}
	return result
}

// ClearMultiKeyTemporaryDisable removes one key cooldown and releases the pool for reevaluation.
func ClearMultiKeyTemporaryDisable(channelId int, key string) bool {
	if channelId <= 0 || key == "" || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	removed := int64(0)
	patterns := []string{
		multiKeyTemporaryDisableKey(channelId, key) + "*",
		fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:scope:key:%s:*", channelId, MultiKeyFingerprint(key)),
	}
	for _, pattern := range patterns {
		var cursor uint64
		for {
			names, next, err := common.RDB.Scan(context.Background(), cursor, pattern, 100).Result()
			if err != nil {
				logChannelAutoDisableRedisError(err)
				return false
			}
			if len(names) > 0 {
				count, err := common.RDB.Del(context.Background(), names...).Result()
				if err != nil {
					logChannelAutoDisableRedisError(err)
					return false
				}
				removed += count
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	_, err := common.RDB.Del(context.Background(), multiKeyPoolBlockedKey(channelId)).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
	}
	var cursor uint64
	for {
		names, next, scanErr := common.RDB.Scan(context.Background(), cursor, multiKeyPoolBlockedKey(channelId)+":model:*", 100).Result()
		if scanErr != nil {
			logChannelAutoDisableRedisError(scanErr)
			break
		}
		if len(names) > 0 {
			if _, deleteErr := common.RDB.Del(context.Background(), names...).Result(); deleteErr != nil {
				logChannelAutoDisableRedisError(deleteErr)
				break
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return removed > 0
}

// ClearAllMultiKeyTemporaryDisable removes all cooldown state for one channel.
func ClearAllMultiKeyTemporaryDisable(channelId int) bool {
	if channelId <= 0 || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	ctx := context.Background()
	patterns := []string{
		fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:cooldown:*", channelId),
		fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:scope:key:*", channelId),
	}
	removed := int64(0)
	for _, pattern := range patterns {
		var cursor uint64
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
	}
	return removed > 0
}

// IsMultiKeyPoolTemporarilyDisabled reports whether every usable key is cooling down.
func IsMultiKeyPoolTemporarilyDisabled(channelId int, upstreamModels ...string) bool {
	if channelId <= 0 || !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	keys := []string{multiKeyPoolBlockedKey(channelId)}
	if len(upstreamModels) > 0 && strings.TrimSpace(upstreamModels[0]) != "" {
		keys = append(keys, multiKeyPoolBlockedKey(channelId, upstreamModels[0]))
	}
	exists, err := common.RDB.Exists(context.Background(), keys...).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return false
	}
	if exists == 0 && len(upstreamModels) == 0 {
		pattern := multiKeyPoolBlockedKey(channelId) + ":model:*"
		var cursor uint64
		for {
			names, next, scanErr := common.RDB.Scan(context.Background(), cursor, pattern, 100).Result()
			if scanErr != nil {
				logChannelAutoDisableRedisError(scanErr)
				return false
			}
			if len(names) > 0 {
				return true
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
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

func refreshMultiKeyPoolBlock(channelId int, upstreamModels ...string) {
	if channelId <= 0 || !common.RedisEnabled || common.RDB == nil {
		return
	}
	channel, err := model.CacheGetChannel(channelId)
	if err != nil || channel == nil || !channel.ChannelInfo.IsMultiKey {
		return
	}
	channel = channel.Snapshot()
	temporary := LoadMultiKeyTemporaryDisableInfo(channel)
	upstreamModel := ""
	if len(upstreamModels) > 0 {
		upstreamModel = upstreamModels[0]
	}
	keys := channel.GetKeys()
	earliest := int64(0)
	hasUsableKey := false
	for index, key := range keys {
		status := common.ChannelStatusEnabled
		if storedStatus, exists := channel.ChannelInfo.MultiKeyStatusList[index]; exists {
			status = storedStatus
		}
		if status != common.ChannelStatusEnabled {
			continue
		}
		if upstreamModel != "" {
			records, err := loadKeyCooldownRecords(channel, key, upstreamModel)
			if err != nil || len(records) == 0 {
				hasUsableKey = true
				break
			}
			blocked := false
			for _, record := range records {
				if record.info.DisabledUntil > time.Now().Unix() {
					blocked = true
					if earliest == 0 || record.info.DisabledUntil < earliest {
						earliest = record.info.DisabledUntil
					}
					continue
				}
				exists, probeErr := common.RDB.Exists(context.Background(), record.name+":probe").Result()
				if probeErr != nil {
					logChannelAutoDisableRedisError(probeErr)
					hasUsableKey = true
					break
				}
				if exists > 0 {
					blocked = true
				}
			}
			if hasUsableKey {
				break
			}
			if !blocked {
				hasUsableKey = true
				break
			}
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
	ctx := context.Background()
	blockKey := multiKeyPoolBlockedKey(channelId, upstreamModel)
	if hasUsableKey || earliest == 0 {
		if deleteErr := common.RDB.Del(ctx, blockKey).Err(); deleteErr != nil {
			logChannelAutoDisableRedisError(deleteErr)
		}
		return
	}
	ttl := time.Until(time.Unix(earliest, 0)) + time.Second
	if ttl <= 0 {
		return
	}
	created, setErr := common.RDB.SetNX(ctx, blockKey, earliest, ttl).Result()
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
	return fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:cooldown:key:%s", channelId, MultiKeyFingerprint(key))
}

func multiKeyPoolBlockedKey(channelId int, upstreamModels ...string) string {
	key := fmt.Sprintf("newapi:channel-auto-disable:v2:{%d}:cooldown:pool-blocked", channelId)
	if len(upstreamModels) > 0 && strings.TrimSpace(upstreamModels[0]) != "" {
		return key + ":model:" + MultiKeyFingerprint(normalizeMultiKeyHealthModel(upstreamModels[0]))
	}
	return key
}
