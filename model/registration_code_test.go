package model

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestConsumeRegistrationCodeTxRequired verifies the global admission boundary.
func TestConsumeRegistrationCodeTxRequired(t *testing.T) {
	truncateTables(t)
	original := common.RegistrationCodeRequired
	common.RegistrationCodeRequired = true
	t.Cleanup(func() { common.RegistrationCodeRequired = original })

	err := DB.Transaction(func(tx *gorm.DB) error {
		return ConsumeRegistrationCodeTx(tx, "", 1, "alice", "password")
	})
	assert.ErrorIs(t, err, ErrRegistrationCodeRequired)
}

// TestRegistrationCodeRequiredOption verifies persisted option updates reach runtime enforcement.
func TestRegistrationCodeRequiredOption(t *testing.T) {
	original := common.RegistrationCodeRequired
	originalOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	t.Cleanup(func() {
		common.RegistrationCodeRequired = original
		common.OptionMap = originalOptionMap
	})

	require.NoError(t, updateOptionMap("RegistrationCodeRequired", "true"))
	assert.True(t, common.RegistrationCodeRequired)
	require.NoError(t, updateOptionMap("RegistrationCodeRequired", "false"))
	assert.False(t, common.RegistrationCodeRequired)
}

// TestConsumeRegistrationCodeTxValidation verifies each managed code state is enforced.
func TestConsumeRegistrationCodeTxValidation(t *testing.T) {
	original := common.RegistrationCodeRequired
	common.RegistrationCodeRequired = true
	t.Cleanup(func() { common.RegistrationCodeRequired = original })

	now := common.GetTimestamp()
	tests := []struct {
		name    string
		code    *RegistrationCode
		input   string
		expects error
	}{
		{name: "unknown", input: "missing", expects: ErrRegistrationCodeInvalid},
		{name: "disabled", input: "disabled", code: &RegistrationCode{Code: "disabled", Status: common.RegistrationCodeStatusDisabled, MaxUses: 1, OpenTime: now - 1}, expects: ErrRegistrationCodeDisabled},
		{name: "not open", input: "not-open", code: &RegistrationCode{Code: "not-open", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now + 60}, expects: ErrRegistrationCodeNotOpen},
		{name: "expired", input: "expired", code: &RegistrationCode{Code: "expired", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now - 60, EndTime: now - 1}, expects: ErrRegistrationCodeExpired},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, DB.Exec("DELETE FROM registration_code_usages").Error)
			require.NoError(t, DB.Exec("DELETE FROM registration_codes").Error)
			if test.code != nil {
				test.code.Name = test.name
				test.code.CreatedTime = now
				require.NoError(t, DB.Create(test.code).Error)
			}
			err := DB.Transaction(func(tx *gorm.DB) error {
				return ConsumeRegistrationCodeTx(tx, test.input, 1, "alice", "password")
			})
			assert.ErrorIs(t, err, test.expects)
		})
	}
}

// TestConsumeRegistrationCodeTxRollback verifies user creation failures do not consume a code.
func TestConsumeRegistrationCodeTxRollback(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	code := RegistrationCode{Code: "rollback-code", Name: "rollback", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now - 1, CreatedTime: now}
	require.NoError(t, DB.Create(&code).Error)

	sentinel := errors.New("rollback transaction")
	err := DB.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, ConsumeRegistrationCodeTx(tx, code.Code, 7, "alice", "password"))
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)

	var stored RegistrationCode
	require.NoError(t, DB.First(&stored, code.Id).Error)
	assert.Zero(t, stored.UsedCount)
	var usages int64
	require.NoError(t, DB.Model(&RegistrationCodeUsage{}).Count(&usages).Error)
	assert.Zero(t, usages)
}

// TestConsumeRegistrationCodeTxConcurrentLimit verifies atomic consumption cannot exceed max_uses.
func TestConsumeRegistrationCodeTxConcurrentLimit(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	code := RegistrationCode{Code: "limited-code", Name: "limited", Status: common.RegistrationCodeStatusEnabled, MaxUses: 2, OpenTime: now - 1, CreatedTime: now}
	require.NoError(t, DB.Create(&code).Error)

	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(userId int) {
			defer wg.Done()
			err := DB.Transaction(func(tx *gorm.DB) error {
				return ConsumeRegistrationCodeTx(tx, code.Code, userId, "user", "password")
			})
			if err == nil {
				successes.Add(1)
				return
			}
			assert.ErrorIs(t, err, ErrRegistrationCodeExhausted)
		}(i + 1)
	}
	wg.Wait()

	assert.Equal(t, int32(2), successes.Load())
	var stored RegistrationCode
	require.NoError(t, DB.First(&stored, code.Id).Error)
	assert.Equal(t, 2, stored.UsedCount)
	var usages int64
	require.NoError(t, DB.Model(&RegistrationCodeUsage{}).Count(&usages).Error)
	assert.EqualValues(t, 2, usages)
}

// TestGetRegistrationCodesFilters verifies management filters and pagination totals.
func TestGetRegistrationCodesFilters(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	codes := []RegistrationCode{
		{Code: "alpha-unused", Name: "Alpha", Status: common.RegistrationCodeStatusEnabled, MaxUses: 3, OpenTime: now - 60, CreatedTime: now},
		{Code: "partial", Name: "Partial", Status: common.RegistrationCodeStatusEnabled, MaxUses: 3, UsedCount: 1, OpenTime: now - 60, CreatedTime: now},
		{Code: "exhausted", Name: "Exhausted", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, UsedCount: 1, OpenTime: now - 60, CreatedTime: now},
		{Code: "pending", Name: "Pending", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now + 60, CreatedTime: now},
		{Code: "expired", Name: "Expired", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now - 120, EndTime: now - 60, CreatedTime: now},
		{Code: "disabled", Name: "Disabled", Status: common.RegistrationCodeStatusDisabled, MaxUses: 1, OpenTime: now - 60, CreatedTime: now},
	}
	require.NoError(t, DB.Create(&codes).Error)

	tests := []struct {
		name   string
		filter RegistrationCodeQuery
		codes  []string
	}{
		{name: "keyword", filter: RegistrationCodeQuery{Keyword: "alpha", Now: now}, codes: []string{"alpha-unused"}},
		{name: "disabled", filter: RegistrationCodeQuery{Status: common.RegistrationCodeStatusDisabled, Now: now}, codes: []string{"disabled"}},
		{name: "unused", filter: RegistrationCodeQuery{Usage: "unused", Now: now}, codes: []string{"disabled", "expired", "pending", "alpha-unused"}},
		{name: "partial", filter: RegistrationCodeQuery{Usage: "partial", Now: now}, codes: []string{"partial"}},
		{name: "exhausted", filter: RegistrationCodeQuery{Usage: "exhausted", Now: now}, codes: []string{"exhausted"}},
		{name: "pending", filter: RegistrationCodeQuery{Validity: "pending", Now: now}, codes: []string{"pending"}},
		{name: "active", filter: RegistrationCodeQuery{Validity: "active", Now: now}, codes: []string{"disabled", "exhausted", "partial", "alpha-unused"}},
		{name: "expired", filter: RegistrationCodeQuery{Validity: "expired", Now: now}, codes: []string{"expired"}},
		{name: "combined", filter: RegistrationCodeQuery{Status: common.RegistrationCodeStatusEnabled, Usage: "unused", Validity: "active", Now: now}, codes: []string{"alpha-unused"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items, total, err := GetRegistrationCodes(test.filter, 0, 100)
			require.NoError(t, err)
			actual := make([]string, 0, len(items))
			for _, item := range items {
				actual = append(actual, item.Code)
			}
			assert.Equal(t, test.codes, actual)
			assert.EqualValues(t, len(test.codes), total)
		})
	}

	items, total, err := GetRegistrationCodes(RegistrationCodeQuery{Usage: "unused", Now: now}, 0, 1)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.EqualValues(t, 4, total)

	boundaries := []RegistrationCode{
		{Code: "boundary-open", Name: "Boundary Open", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now, CreatedTime: now},
		{Code: "boundary-end", Name: "Boundary End", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now - 60, EndTime: now, CreatedTime: now},
	}
	require.NoError(t, DB.Create(&boundaries).Error)
	items, total, err = GetRegistrationCodes(RegistrationCodeQuery{Keyword: "BOUNDARY", Validity: "active", Now: now}, 0, 100)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.EqualValues(t, 2, total)
}

// TestBatchManageRegistrationCodes verifies selected updates and usage-audit retention.
func TestBatchManageRegistrationCodes(t *testing.T) {
	truncateTables(t)
	now := common.GetTimestamp()
	codes := []RegistrationCode{
		{Code: "batch-a", Name: "A", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now - 1, CreatedTime: now},
		{Code: "batch-b", Name: "B", Status: common.RegistrationCodeStatusDisabled, MaxUses: 1, OpenTime: now - 1, CreatedTime: now},
		{Code: "batch-c", Name: "C", Status: common.RegistrationCodeStatusEnabled, MaxUses: 1, OpenTime: now - 1, CreatedTime: now},
	}
	require.NoError(t, DB.Create(&codes).Error)
	require.NoError(t, DB.Create(&RegistrationCodeUsage{RegistrationCodeId: codes[0].Id, UserId: 7, Username: "alice", Source: "password", UsedTime: now}).Error)

	changed, err := BatchUpdateRegistrationCodeStatus([]int{codes[0].Id, codes[1].Id}, common.RegistrationCodeStatusDisabled)
	require.NoError(t, err)
	assert.EqualValues(t, 1, changed)
	var stored []RegistrationCode
	require.NoError(t, DB.Unscoped().Order("id asc").Find(&stored).Error)
	assert.Equal(t, common.RegistrationCodeStatusDisabled, stored[0].Status)
	assert.Equal(t, common.RegistrationCodeStatusDisabled, stored[1].Status)
	assert.Equal(t, common.RegistrationCodeStatusEnabled, stored[2].Status)

	deleted, err := BatchDeleteRegistrationCodes([]int{codes[0].Id, codes[1].Id})
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)
	var usages int64
	require.NoError(t, DB.Model(&RegistrationCodeUsage{}).Where("registration_code_id = ?", codes[0].Id).Count(&usages).Error)
	assert.EqualValues(t, 1, usages)
}
