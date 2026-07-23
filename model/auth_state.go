package model

import (
	"errors"
	"fmt"
)

// UserAuthState contains database-authoritative fields used by session authorization.
type UserAuthState struct {
	Status   int
	Role     int
	Username string
	Group    string
}

// TokenAuthState contains database-authoritative fields used by API token authorization.
type TokenAuthState struct {
	TokenStatus      int    `gorm:"column:token_status"`
	TokenExpiredTime int64  `gorm:"column:token_expired_time"`
	UserStatus       int    `gorm:"column:user_status"`
	UserGroup        string `gorm:"column:user_group"`
}

// GetUserAuthState reads authorization-sensitive user fields directly from the database.
func GetUserAuthState(userId int) (*UserAuthState, error) {
	if userId <= 0 {
		return nil, errors.New("userId 无效")
	}
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

// GetTokenAuthState reads authorization-sensitive token fields directly from the database.
func GetTokenAuthState(tokenId int, userId int) (*TokenAuthState, error) {
	if tokenId <= 0 || userId <= 0 {
		return nil, errors.New("tokenId 或 userId 无效")
	}
	var state TokenAuthState
	selectFields := fmt.Sprintf(
		"tokens.status AS token_status, tokens.expired_time AS token_expired_time, "+
			"users.status AS user_status, users.%s AS user_group",
		commonGroupCol,
	)
	if err := DB.Table("tokens").
		Select(selectFields).
		Joins("JOIN users ON users.id = tokens.user_id").
		Where("tokens.id = ? AND tokens.user_id = ? AND tokens.deleted_at IS NULL AND users.deleted_at IS NULL",
			tokenId, userId).
		Take(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}
