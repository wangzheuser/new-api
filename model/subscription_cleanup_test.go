package model

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestCleanupSubscriptionPreConsumeRecordsIsBounded verifies expiration ordering and per-run limits.
func TestCleanupSubscriptionPreConsumeRecordsIsBounded(t *testing.T) {
	truncateTables(t)
	records := make([]SubscriptionPreConsumeRecord, 0, 6)
	for i := 1; i <= 5; i++ {
		records = append(records, SubscriptionPreConsumeRecord{
			RequestId: fmt.Sprintf("expired-%d", i),
			Status:    "consumed",
			CreatedAt: int64(i),
			UpdatedAt: int64(i),
		})
	}
	records = append(records, SubscriptionPreConsumeRecord{
		RequestId: "recent",
		Status:    "consumed",
		CreatedAt: 200,
		UpdatedAt: 200,
	})
	require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Create(&records).Error)

	deleted, err := cleanupSubscriptionPreConsumeRecords(100, 2, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 4, deleted)

	var expiredRemaining int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("updated_at < ?", 100).Count(&expiredRemaining).Error)
	assert.EqualValues(t, 1, expiredRemaining)

	deleted, err = cleanupSubscriptionPreConsumeRecords(100, 2, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	var recentRemaining int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", "recent").Count(&recentRemaining).Error)
	assert.EqualValues(t, 1, recentRemaining)
}
