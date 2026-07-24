package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthStateCacheTTLCappedAtOneMinute(t *testing.T) {
	previousFrequency := common.SyncFrequency
	t.Cleanup(func() {
		common.SyncFrequency = previousFrequency
	})

	common.SyncFrequency = 30
	assert.Equal(t, 30*time.Second, authStateCacheTTL())
	common.SyncFrequency = 600
	assert.Equal(t, time.Minute, authStateCacheTTL())
}

func TestAuthStateReadsCurrentDatabaseValuesWithoutRedis(t *testing.T) {
	truncateTables(t)
	user := &User{
		Username: "auth-state-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "国模",
		AffCode:  "auth-state-aff",
	}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         "authstatekey",
		Name:        "auth-state-token",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
	}
	require.NoError(t, DB.Create(token).Error)

	userState, err := GetUserAuthState(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.UserStatusEnabled, userState.Status)
	assert.Equal(t, "国模", userState.Group)

	tokenState, err := GetTokenAuthState(token.Id, user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusEnabled, tokenState.TokenStatus)
	assert.Equal(t, int64(-1), tokenState.TokenExpiredTime)
	assert.Equal(t, user.Id, tokenState.UserId)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"status": common.UserStatusDisabled,
		"group":  "default",
	}).Error)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Update("status", common.TokenStatusDisabled).Error)

	userState, err = GetUserAuthState(user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.UserStatusDisabled, userState.Status)
	assert.Equal(t, "default", userState.Group)

	tokenState, err = GetTokenAuthState(token.Id, user.Id)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusDisabled, tokenState.TokenStatus)
	assert.Equal(t, user.Id, tokenState.UserId)
}

func TestAuthStateRejectsDeletedCredentials(t *testing.T) {
	truncateTables(t)
	user := &User{
		Username: "deleted-auth-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "deleted-auth-aff",
	}
	require.NoError(t, DB.Create(user).Error)
	token := &Token{
		UserId:      user.Id,
		Key:         "deletedauthkey",
		Name:        "deleted-auth-token",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
	}
	require.NoError(t, DB.Create(token).Error)
	require.NoError(t, DB.Delete(token).Error)
	require.NoError(t, DB.Delete(user).Error)

	_, err := GetTokenAuthState(token.Id, user.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = GetUserAuthState(user.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
