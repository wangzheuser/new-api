package main

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type legacyRedemption struct {
	Id        int `gorm:"primaryKey"`
	Quota     int
	Status    int
	DeletedAt gorm.DeletedAt
}

// TableName keeps the historical fixture on the production table name.
func (legacyRedemption) TableName() string {
	return "redemptions"
}

// TestApplyMigration verifies schema addition, historical normalization, and idempotency.
func TestApplyMigration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:redemption-migration?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&legacyRedemption{}, &model.Option{}))
	require.NoError(t, db.Create(&legacyRedemption{
		Quota:  100,
		Status: common.RedemptionCodeStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&legacyRedemption{
		Quota:  0,
		Status: common.RedemptionCodeStatusEnabled,
	}).Error)

	before, err := inspectMigration(db, "sqlite")
	require.NoError(t, err)
	assert.False(t, before.HasPlanIDColumn)
	assert.EqualValues(t, 2, before.TotalCodes)
	assert.EqualValues(t, 1, before.InvalidQuotaCodes)

	applied, err := applyMigration(db, "sqlite")
	require.NoError(t, err)
	assert.True(t, applied.HasPlanIDColumn)
	assert.True(t, applied.AlreadyApplied)
	assert.EqualValues(t, 1, applied.DisabledInvalidCodes)
	assert.Zero(t, applied.InvalidQuotaCodes)

	var rows []model.Redemption
	require.NoError(t, db.Order("id ASC").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Zero(t, rows[0].PlanId)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, rows[0].Status)
	assert.Zero(t, rows[1].PlanId)
	assert.Equal(t, common.RedemptionCodeStatusDisabled, rows[1].Status)

	repeated, err := applyMigration(db, "sqlite")
	require.NoError(t, err)
	assert.True(t, repeated.AlreadyApplied)
	assert.Zero(t, repeated.DisabledInvalidCodes)
}

// TestApplyMigrationRejectsUnmarkedSubscriptionCodes protects post-launch data from destructive reruns.
func TestApplyMigrationRejectsUnmarkedSubscriptionCodes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:redemption-migration-guard?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Redemption{}, &model.Option{}))
	require.NoError(t, db.Create(&model.Redemption{
		Key:    "subscription-code",
		Status: common.RedemptionCodeStatusEnabled,
		PlanId: 12,
	}).Error)

	_, err = applyMigration(db, "sqlite")
	require.ErrorContains(t, err, "subscription redemption codes exist")

	var markerCount int64
	require.NoError(t, db.Model(&model.Option{}).
		Where("key = ?", redemptionSubscriptionMigrationKey).
		Count(&markerCount).Error)
	assert.Zero(t, markerCount)
}
