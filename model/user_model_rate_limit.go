package model

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"

	"github.com/samber/hot"
	"gorm.io/gorm"
)

const userModelRateLimitCacheNamespace = "new-api:user_model_rate_limits:v1"

// UserModelRateLimit stores one request-rate rule for a user and a runtime group bucket.
type UserModelRateLimit struct {
	Id           int     `json:"id" gorm:"primaryKey"`
	UserId       int     `json:"user_id" gorm:"type:int;not null;index;uniqueIndex:uk_user_model_rate_limit,priority:1"`
	GroupName    string  `json:"group" gorm:"column:group_name;type:varchar(64);not null;uniqueIndex:uk_user_model_rate_limit,priority:2"`
	TotalCount   int     `json:"total_count" gorm:"type:int;not null"`
	SuccessCount int     `json:"success_count" gorm:"type:int;not null"`
	StatusCode   *int    `json:"status_code,omitempty" gorm:"type:int"`
	ErrorMessage *string `json:"error_message,omitempty" gorm:"type:text"`
	CreatedAt    int64   `json:"created_at" gorm:"type:bigint"`
	UpdatedAt    int64   `json:"updated_at" gorm:"type:bigint;index"`
}

// UserModelRateLimitWithUser contains the searchable user columns displayed by the management page.
type UserModelRateLimitWithUser struct {
	UserModelRateLimit
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	UserStatus  int    `json:"user_status"`
}

// BeforeCreate initializes rule timestamps.
func (rule *UserModelRateLimit) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	rule.CreatedAt = now
	rule.UpdatedAt = now
	return nil
}

var userModelRateLimitCacheOnce sync.Once
var userModelRateLimitCache *cachex.HybridCache[map[string]UserModelRateLimit]

// userModelRateLimitCacheTTL follows the shared cache cadence while bounding stale local entries.
func userModelRateLimitCacheTTL() time.Duration {
	ttlSeconds := common.RedisKeyCacheSeconds()
	if ttlSeconds <= 0 || ttlSeconds > 60 {
		ttlSeconds = 60
	}
	return time.Duration(ttlSeconds) * time.Second
}

// getUserModelRateLimitCache builds the shared Redis or local-memory cache lazily.
func getUserModelRateLimitCache() *cachex.HybridCache[map[string]UserModelRateLimit] {
	userModelRateLimitCacheOnce.Do(func() {
		ttl := userModelRateLimitCacheTTL()
		userModelRateLimitCache = cachex.NewHybridCache[map[string]UserModelRateLimit](cachex.HybridCacheConfig[map[string]UserModelRateLimit]{
			Namespace: cachex.Namespace(userModelRateLimitCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[map[string]UserModelRateLimit]{},
			Memory: func() *hot.HotCache[string, map[string]UserModelRateLimit] {
				return hot.NewHotCache[string, map[string]UserModelRateLimit](hot.LRU, 10000).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return userModelRateLimitCache
}

// GetUserModelRateLimits loads all group rules for a user, including a cached empty result.
func GetUserModelRateLimits(userId int) (map[string]UserModelRateLimit, error) {
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	cacheKey := strconv.Itoa(userId)
	cache := getUserModelRateLimitCache()
	if rules, found, err := cache.Get(cacheKey); err == nil && found {
		if rules == nil {
			rules = map[string]UserModelRateLimit{}
		}
		return rules, nil
	} else if err != nil {
		common.SysError("failed to read user model rate-limit cache: " + err.Error())
	}

	rows := make([]UserModelRateLimit, 0)
	if err := DB.Where("user_id = ?", userId).Order("group_name asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	rules := make(map[string]UserModelRateLimit, len(rows))
	for _, rule := range rows {
		rules[rule.GroupName] = rule
	}
	if err := cache.SetWithTTL(cacheKey, rules, userModelRateLimitCacheTTL()); err != nil {
		common.SysError("failed to populate user model rate-limit cache: " + err.Error())
	}
	return rules, nil
}

// InvalidateUserModelRateLimitCache removes one user's complete rule map.
func InvalidateUserModelRateLimitCache(userId int) error {
	if userId <= 0 {
		return nil
	}
	_, err := getUserModelRateLimitCache().DeleteMany([]string{strconv.Itoa(userId)})
	return err
}

// GetUserModelRateLimitById returns one management rule.
func GetUserModelRateLimitById(id int) (*UserModelRateLimit, error) {
	if id <= 0 {
		return nil, errors.New("invalid rule id")
	}
	var rule UserModelRateLimit
	if err := DB.First(&rule, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// UserModelRateLimitExists reports whether the unique user and group pair already exists.
func UserModelRateLimitExists(userId int, group string, excludeId int) (bool, error) {
	query := DB.Model(&UserModelRateLimit{}).Where("user_id = ? AND group_name = ?", userId, group)
	if excludeId > 0 {
		query = query.Where("id <> ?", excludeId)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// CreateUserModelRateLimit persists one normalized rule and invalidates its runtime cache.
func CreateUserModelRateLimit(rule *UserModelRateLimit) error {
	if rule == nil {
		return errors.New("rule is nil")
	}
	if err := DB.Create(rule).Error; err != nil {
		return err
	}
	if err := InvalidateUserModelRateLimitCache(rule.UserId); err != nil {
		common.SysError("failed to invalidate user model rate-limit cache after create: " + err.Error())
	}
	return nil
}

// UpdateUserModelRateLimit updates mutable rule fields while preserving its owner.
func UpdateUserModelRateLimit(rule *UserModelRateLimit) error {
	if rule == nil || rule.Id <= 0 {
		return errors.New("invalid rule")
	}
	rule.UpdatedAt = common.GetTimestamp()
	updates := map[string]interface{}{
		"group_name":    rule.GroupName,
		"total_count":   rule.TotalCount,
		"success_count": rule.SuccessCount,
		"status_code":   rule.StatusCode,
		"error_message": rule.ErrorMessage,
		"updated_at":    rule.UpdatedAt,
	}
	if err := DB.Model(&UserModelRateLimit{}).Where("id = ?", rule.Id).Updates(updates).Error; err != nil {
		return err
	}
	if err := InvalidateUserModelRateLimitCache(rule.UserId); err != nil {
		common.SysError("failed to invalidate user model rate-limit cache after update: " + err.Error())
	}
	return nil
}

// DeleteUserModelRateLimit removes one rule and invalidates the owning user's cache.
func DeleteUserModelRateLimit(rule *UserModelRateLimit) error {
	if rule == nil || rule.Id <= 0 {
		return errors.New("invalid rule")
	}
	if err := DB.Delete(&UserModelRateLimit{}, "id = ?", rule.Id).Error; err != nil {
		return err
	}
	if err := InvalidateUserModelRateLimitCache(rule.UserId); err != nil {
		common.SysError("failed to invalidate user model rate-limit cache after delete: " + err.Error())
	}
	return nil
}

// SearchUserModelRateLimits returns active-user rules for the root management table.
func SearchUserModelRateLimits(keyword, group string, offset, limit int) ([]UserModelRateLimitWithUser, int64, error) {
	query := DB.Table("user_model_rate_limits AS rules").
		Joins("JOIN users ON users.id = rules.user_id AND users.deleted_at IS NULL")

	keyword = strings.TrimSpace(keyword)
	if keyword != "" {
		pattern := "%" + strings.ToLower(keyword) + "%"
		condition := "LOWER(users.username) LIKE ? OR LOWER(users.display_name) LIKE ? OR LOWER(users.email) LIKE ? OR LOWER(rules.group_name) LIKE ? OR LOWER(COALESCE(rules.error_message, '')) LIKE ?"
		args := []interface{}{pattern, pattern, pattern, pattern, pattern}
		if userId, err := strconv.Atoi(keyword); err == nil {
			condition = "rules.user_id = ? OR " + condition
			args = append([]interface{}{userId}, args...)
		}
		query = query.Where("("+condition+")", args...)
	}
	group = strings.TrimSpace(group)
	if group != "" {
		query = query.Where("rules.group_name = ?", group)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]UserModelRateLimitWithUser, 0)
	err := query.Select("rules.*, users.username, users.display_name, users.email, users.status AS user_status").
		Order("rules.updated_at DESC, rules.id DESC").
		Offset(offset).
		Limit(limit).
		Scan(&items).Error
	return items, total, err
}

// deleteUserModelRateLimitsByUserId removes dependent rules inside a user hard-delete transaction.
func deleteUserModelRateLimitsByUserId(tx *gorm.DB, userId int) error {
	return tx.Where("user_id = ?", userId).Delete(&UserModelRateLimit{}).Error
}
