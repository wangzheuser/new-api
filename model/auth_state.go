package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// UserAuthState contains authorization-sensitive user fields.
type UserAuthState struct {
	Status   int
	Role     int
	Username string
	Group    string
	Deleted  bool
}

// TokenAuthState contains authorization-sensitive token fields.
type TokenAuthState struct {
	TokenStatus      int
	TokenExpiredTime int64
	UserId           int
	Deleted          bool
}

func authStateCacheTTL() time.Duration {
	ttl := time.Duration(common.RedisKeyCacheSeconds()) * time.Second
	if ttl <= 0 || ttl > time.Minute {
		return time.Minute
	}
	return ttl
}

func getAuthStateCache(key string, state any) error {
	raw, err := common.RedisGet(key)
	if err != nil {
		return err
	}
	return common.UnmarshalJsonStr(raw, state)
}

func setAuthStateCache(key string, state any, onlyIfAbsent bool) error {
	data, err := common.Marshal(state)
	if err != nil {
		return err
	}
	if onlyIfAbsent {
		_, err = common.RedisSetNX(key, string(data), authStateCacheTTL())
		return err
	}
	return common.RedisSet(key, string(data), authStateCacheTTL())
}

func loadUserAuthState(userId int) (*UserAuthState, error) {
	var user User
	if err := DB.Select([]string{"status", "role", "username", "group"}).
		Where("id = ?", userId).
		Take(&user).Error; err != nil {
		return nil, err
	}
	return &UserAuthState{
		Status:   user.Status,
		Role:     user.Role,
		Username: user.Username,
		Group:    user.Group,
	}, nil
}

func loadTokenAuthState(tokenId int, userId int) (*TokenAuthState, error) {
	var token Token
	if err := DB.Select([]string{"status", "expired_time", "user_id"}).
		Where("id = ? AND user_id = ?", tokenId, userId).
		Take(&token).Error; err != nil {
		return nil, err
	}
	return &TokenAuthState{
		TokenStatus:      token.Status,
		TokenExpiredTime: token.ExpiredTime,
		UserId:           token.UserId,
	}, nil
}

func userAuthStateCacheKey(userId int) string {
	return fmt.Sprintf("auth:user:%d", userId)
}

func tokenAuthStateCacheKey(tokenId int) string {
	return fmt.Sprintf("auth:token:%d", tokenId)
}

// GetUserAuthState reads user authorization state from Redis and falls back to the database.
func GetUserAuthState(userId int) (*UserAuthState, error) {
	if userId <= 0 {
		return nil, errors.New("userId 无效")
	}
	if common.RedisEnabled {
		var cached UserAuthState
		if err := getAuthStateCache(userAuthStateCacheKey(userId), &cached); err == nil {
			if cached.Deleted {
				return nil, gorm.ErrRecordNotFound
			}
			return &cached, nil
		}
	}
	state, err := loadUserAuthState(userId)
	if err != nil {
		if common.RedisEnabled && errors.Is(err, gorm.ErrRecordNotFound) {
			_ = setAuthStateCache(userAuthStateCacheKey(userId), &UserAuthState{Deleted: true}, true)
		}
		return nil, err
	}
	if common.RedisEnabled {
		if err := setAuthStateCache(userAuthStateCacheKey(userId), state, true); err != nil {
			common.SysLog(fmt.Sprintf("failed to populate user auth cache for user %d: %v", userId, err))
		}
	}
	return state, nil
}

// RefreshUserAuthStateCache writes the latest committed user authorization state to Redis.
func RefreshUserAuthStateCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return errors.New("userId 无效")
	}
	state, err := loadUserAuthState(userId)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		state = &UserAuthState{Deleted: true}
	}
	return setAuthStateCache(userAuthStateCacheKey(userId), state, false)
}

// GetTokenAuthState reads token authorization state from Redis and falls back to the database.
func GetTokenAuthState(tokenId int, userId int) (*TokenAuthState, error) {
	if tokenId <= 0 || userId <= 0 {
		return nil, errors.New("tokenId 或 userId 无效")
	}
	if common.RedisEnabled {
		var cached TokenAuthState
		if err := getAuthStateCache(tokenAuthStateCacheKey(tokenId), &cached); err == nil {
			if cached.Deleted || cached.UserId != userId {
				return nil, gorm.ErrRecordNotFound
			}
			return &cached, nil
		}
	}
	state, err := loadTokenAuthState(tokenId, userId)
	if err != nil {
		if common.RedisEnabled && errors.Is(err, gorm.ErrRecordNotFound) {
			_ = setAuthStateCache(tokenAuthStateCacheKey(tokenId), &TokenAuthState{
				UserId:  userId,
				Deleted: true,
			}, true)
		}
		return nil, err
	}
	if common.RedisEnabled {
		if err := setAuthStateCache(tokenAuthStateCacheKey(tokenId), state, true); err != nil {
			common.SysLog(fmt.Sprintf("failed to populate token auth cache for token %d: %v", tokenId, err))
		}
	}
	return state, nil
}

// RefreshTokenAuthStateCache writes the latest committed token authorization state to Redis.
func RefreshTokenAuthStateCache(tokenId int, userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if tokenId <= 0 || userId <= 0 {
		return errors.New("tokenId 或 userId 无效")
	}
	state, err := loadTokenAuthState(tokenId, userId)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		state = &TokenAuthState{
			UserId:  userId,
			Deleted: true,
		}
	}
	return setAuthStateCache(tokenAuthStateCacheKey(tokenId), state, false)
}
