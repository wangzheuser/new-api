package service

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const recordKeyCooldown = `
local old = redis.call('GET', KEYS[1])
local n = 1
local previousUntil = 0
if old then local ok, v = pcall(cjson.decode, old); if ok then n = math.min((v.failures or 0)+1, 32); previousUntil = v.disabled_until or 0 end end
local v = cjson.decode(ARGV[1])
local now = tonumber(ARGV[2])
local delay = math.min(tonumber(ARGV[3]) * 2^(n-1), 86400)
if tonumber(ARGV[4]) > now then delay = tonumber(ARGV[4])-now
else delay = math.min(math.ceil(delay * (1+tonumber(ARGV[5]))), 86400) end
delay = math.max(delay, previousUntil-now)
v.failures=n; v.disabled_until=now+delay
redis.call('SET', KEYS[1], cjson.encode(v), 'EX', delay+86400)
return v.disabled_until`

const acquireKeyProbe = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('SET', KEYS[2], ARGV[2], 'NX', 'EX', 60) then return 1 end
return 0`

const finishKeyProbe = `
if redis.call('GET', KEYS[2]) ~= ARGV[2] then return 0 end
if ARGV[3] == '1' and redis.call('GET', KEYS[1]) == ARGV[1] then redis.call('DEL', KEYS[1]) end
return redis.call('DEL', KEYS[2])`

const renewKeyProbe = `
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('EXPIRE', KEYS[1], 60) end
return 0`

type keyCooldownRecord struct {
	name, raw string
	info      dto.MultiKeyTemporaryDisableInfo
}

type channelKeyProbe struct {
	client  *redis.Client
	records []keyCooldownRecord
	token   string
	once    sync.Once
	stop    chan struct{}
	done    chan struct{}
}

// multiKeyModelDisableKey keeps models isolated while retaining the legacy whole-key namespace.
func multiKeyModelDisableKey(channelID int, key, upstreamModel string) string {
	return multiKeyTemporaryDisableKey(channelID, key) + ":model:" + MultiKeyFingerprint(upstreamModel)
}

// writeMultiKeyCooldown atomically advances failure history without shortening a provider reset hint.
func writeMultiKeyCooldown(channel *model.Channel, key string, d MultiKeyDecision, reason string) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	name := multiKeyTemporaryDisableKey(channel.Id, key)
	if d.Scope == "model" {
		name = multiKeyModelDisableKey(channel.Id, key, d.Model)
	}
	info := dto.MultiKeyTemporaryDisableInfo{StatusCode: d.StatusCode, Reason: reason, Scope: d.Scope, Model: d.Model, Category: d.Category, Source: d.Source, Version: common.NewRequestId()}
	raw, err := common.Marshal(info)
	if err != nil {
		common.SysError("failed to encode key cooldown")
		return
	}
	_, err = common.RDB.Eval(context.Background(), recordKeyCooldown, []string{name}, string(raw), time.Now().Unix(), effectiveMultiKeyAutoDisableConfig(channel).TemporaryDisableMinutes*60, d.RecoverAt, rand.Float64()*0.1).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
	}
}

// loadKeyCooldownRecords reads only the whole-key and requested model state on the routing path.
func loadKeyCooldownRecords(channelID int, key, upstreamModel string) ([]keyCooldownRecord, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return nil, nil
	}
	names := []string{multiKeyTemporaryDisableKey(channelID, key)}
	if upstreamModel != "" {
		names = append(names, multiKeyModelDisableKey(channelID, key, upstreamModel))
	}
	values, err := common.RDB.MGet(context.Background(), names...).Result()
	if err != nil {
		logChannelAutoDisableRedisError(err)
		return nil, err
	}
	var records []keyCooldownRecord
	for i, v := range values {
		raw, ok := v.(string)
		if !ok {
			continue
		}
		var info dto.MultiKeyTemporaryDisableInfo
		if common.UnmarshalJsonStr(raw, &info) != nil {
			continue
		}
		if info.Scope == "" {
			info.Scope = "key"
		}
		records = append(records, keyCooldownRecord{name: names[i], raw: raw, info: info})
	}
	return records, nil
}

// SelectChannelKeyForRequest selects a ready key or leases one half-open recovery attempt.
func SelectChannelKeyForRequest(c *gin.Context, channel *model.Channel, upstreamModel string, excluded map[string]struct{}) (string, int, *types.NewAPIError) {
	FinishChannelKeyProbe(c, false)
	if !channel.ChannelInfo.IsMultiKey {
		return channel.GetNextEnabledKey()
	}
	indexes := make(map[int]struct{})
	keys := channel.GetKeys()
	for i, key := range keys {
		if _, ok := excluded[MultiKeyFingerprint(key)]; ok {
			indexes[i] = struct{}{}
		}
	}
	if !channel.GetAutoBan() || !common.AutomaticDisableChannelEnabled {
		return channel.GetNextEnabledKeyExcluding(indexes)
	}
	for len(indexes) < len(keys) {
		key, index, apiErr := channel.GetNextEnabledKeyExcluding(indexes)
		if apiErr != nil {
			return "", 0, apiErr
		}
		records, readErr := loadKeyCooldownRecords(channel.Id, key, upstreamModel)
		if readErr != nil || len(records) == 0 {
			return key, index, nil
		}
		blocked := false
		for _, r := range records {
			if r.info.DisabledUntil > time.Now().Unix() {
				blocked = true
			}
		}
		if blocked {
			indexes[index] = struct{}{}
			continue
		}
		leaseUnavailable := false
		p := &channelKeyProbe{client: common.RDB, token: common.NewRequestId(), stop: make(chan struct{}), done: make(chan struct{})}
		for _, r := range records {
			ok, err := p.client.Eval(c.Request.Context(), acquireKeyProbe, []string{r.name, r.name + ":probe"}, r.raw, p.token).Int()
			if err != nil {
				logChannelAutoDisableRedisError(err)
				leaseUnavailable = true
			}
			if err != nil || ok != 1 {
				blocked = true
				break
			}
			p.records = append(p.records, r)
		}
		if blocked {
			for _, r := range p.records {
				_, _ = p.client.Eval(context.Background(), finishKeyProbe, []string{r.name, r.name + ":probe"}, r.raw, p.token, "0").Result()
			}
			if leaseUnavailable {
				return key, index, nil // Redis 故障时保留请求内排除，不阻断可用密钥。
			}
			indexes[index] = struct{}{}
			continue
		}
		c.Set(string(constant.ContextKeyChannelKeyProbe), p)
		go p.renew(c.Request.Context())
		return key, index, nil
	}
	return "", 0, types.NewError(fmt.Errorf("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
}

// renew keeps a recovery lease alive only for the lifetime of its owning request.
func (p *channelKeyProbe) renew(ctx context.Context) {
	defer close(p.done)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ctx.Done():
			// 取消请求只释放自身租约，不将取消误记为恢复成功。
			for _, r := range p.records {
				_, _ = p.client.Eval(context.Background(), finishKeyProbe, []string{r.name, r.name + ":probe"}, r.raw, p.token, "0").Result()
			}
			return
		case <-ticker.C:
			for _, r := range p.records {
				if _, err := p.client.Eval(ctx, renewKeyProbe, []string{r.name + ":probe"}, p.token).Result(); err != nil {
					logChannelAutoDisableRedisError(err)
				}
			}
		}
	}
}

// FinishChannelKeyProbe releases an attempt and clears only the state version it actually tested.
func FinishChannelKeyProbe(c *gin.Context, success bool) {
	value, ok := c.Get(string(constant.ContextKeyChannelKeyProbe))
	if !ok {
		return
	}
	p, ok := value.(*channelKeyProbe)
	if !ok || p == nil {
		return
	}
	p.once.Do(func() {
		close(p.stop)
		<-p.done
		flag := "0"
		if success {
			flag = "1"
		}
		for _, r := range p.records {
			if _, err := p.client.Eval(context.Background(), finishKeyProbe, []string{r.name, r.name + ":probe"}, r.raw, p.token, flag).Result(); err != nil {
				logChannelAutoDisableRedisError(err)
			}
		}
	})
	c.Set(string(constant.ContextKeyChannelKeyProbe), nil)
}

// LoadMultiKeyCooldowns returns all scopes for management, without putting SCAN on the relay path.
func LoadMultiKeyCooldowns(channelID int, key string) []dto.MultiKeyTemporaryDisableInfo {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	var out []dto.MultiKeyTemporaryDisableInfo
	var cursor uint64
	for {
		names, next, err := common.RDB.Scan(context.Background(), cursor, multiKeyTemporaryDisableKey(channelID, key)+"*", 100).Result()
		if err != nil {
			logChannelAutoDisableRedisError(err)
			return out
		}
		for _, name := range names {
			if strings.HasSuffix(name, ":probe") {
				continue
			}
			raw, err := common.RDB.Get(context.Background(), name).Result()
			if err != nil {
				continue
			}
			var info dto.MultiKeyTemporaryDisableInfo
			if common.UnmarshalJsonStr(raw, &info) != nil {
				continue
			}
			if info.Scope == "" {
				info.Scope = "key"
			}
			info.State = "cooling"
			if info.DisabledUntil <= time.Now().Unix() {
				info.State = "pending_probe"
			}
			out = append(out, info)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return out
}

// ClearMultiKeyModelCooldown clears only one model and its recovery lease.
func ClearMultiKeyModelCooldown(channelID int, key, upstreamModel string) error {
	if !common.RedisEnabled || common.RDB == nil {
		return fmt.Errorf("Redis is unavailable")
	}
	name := multiKeyModelDisableKey(channelID, key, upstreamModel)
	return common.RDB.Del(context.Background(), name, name+":probe").Err()
}

// IsMultiKeyModelPoolBlocked checks model eligibility without reserving a recovery probe.
func IsMultiKeyModelPoolBlocked(channel *model.Channel, upstreamModel string) bool {
	if channel == nil || !channel.ChannelInfo.IsMultiKey || !channel.GetAutoBan() || !common.AutomaticDisableChannelEnabled || !common.RedisEnabled || common.RDB == nil {
		return false
	}
	snapshot := channel.Snapshot()
	for index, key := range snapshot.GetKeys() {
		if status, ok := snapshot.ChannelInfo.MultiKeyStatusList[index]; ok && status != common.ChannelStatusEnabled {
			continue
		}
		records, err := loadKeyCooldownRecords(channel.Id, key, upstreamModel)
		if err != nil {
			return false
		}
		blocked := false
		for _, r := range records {
			if r.info.DisabledUntil > time.Now().Unix() {
				blocked = true
				break
			}
			exists, err := common.RDB.Exists(context.Background(), r.name+":probe").Result()
			if err != nil {
				logChannelAutoDisableRedisError(err)
				return false
			}
			if exists > 0 {
				blocked = true
				break
			}
		}
		if !blocked {
			return false
		}
	}
	return true
}
