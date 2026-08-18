package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestUserModelRateLimitPersistence covers the unique rule contract, cache invalidation, and active-user search.
func TestUserModelRateLimitPersistence(t *testing.T) {
	previousDB := DB
	previousDBType := common.MainDatabaseType()
	dsn := fmt.Sprintf("file:user-model-rate-limit-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, DB.AutoMigrate(&User{}, &UserModelRateLimit{}))
	assert.True(t, DB.Migrator().HasIndex(&UserModelRateLimit{}, "uk_user_model_rate_limit"))

	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousDBType)
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	user := User{
		Username:    "rate-limit-user",
		Password:    "password",
		DisplayName: "Rate Limit User",
		Email:       "rate-limit@example.com",
		Group:       "vip",
		Status:      common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&user).Error)
	rule := &UserModelRateLimit{
		UserId:       user.Id,
		GroupName:    "vip",
		TotalCount:   10,
		SuccessCount: 8,
	}
	require.NoError(t, CreateUserModelRateLimit(rule))
	assert.NotZero(t, rule.Id)
	assert.NotZero(t, rule.CreatedAt)

	duplicate := &UserModelRateLimit{
		UserId:       user.Id,
		GroupName:    "vip",
		TotalCount:   20,
		SuccessCount: 10,
	}
	require.Error(t, DB.Create(duplicate).Error)

	rules, err := GetUserModelRateLimits(user.Id)
	require.NoError(t, err)
	require.Contains(t, rules, "vip")
	assert.Equal(t, 10, rules["vip"].TotalCount)

	statusCode := 403
	errorMessage := "custom response"
	rule.GroupName = "enterprise"
	rule.TotalCount = 25
	rule.StatusCode = &statusCode
	rule.ErrorMessage = &errorMessage
	require.NoError(t, UpdateUserModelRateLimit(rule))
	rules, err = GetUserModelRateLimits(user.Id)
	require.NoError(t, err)
	assert.NotContains(t, rules, "vip")
	require.Contains(t, rules, "enterprise")
	assert.Equal(t, 25, rules["enterprise"].TotalCount)

	items, total, err := SearchUserModelRateLimits("rate-limit@example.com", "enterprise", 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, user.Id, items[0].UserId)
	assert.Equal(t, "Rate Limit User", items[0].DisplayName)

	// Soft deletion hides the rule from management without removing it.
	require.NoError(t, DB.Delete(&user).Error)
	items, total, err = SearchUserModelRateLimits("", "", 0, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, items)
	var storedCount int64
	require.NoError(t, DB.Model(&UserModelRateLimit{}).Where("user_id = ?", user.Id).Count(&storedCount).Error)
	assert.EqualValues(t, 1, storedCount)

	require.NoError(t, DeleteUserModelRateLimit(rule))
	rules, err = GetUserModelRateLimits(user.Id)
	require.NoError(t, err)
	assert.Empty(t, rules)
}
