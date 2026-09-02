package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSearchRedemptionsFiltersAndPaginates(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	})

	now := common.GetTimestamp()
	redemptions := []Redemption{
		{Id: 1, Name: "alpha-active", Key: "00000000000000000000000000000001", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: 0},
		{Id: 2, Name: "alpha-future", Key: "00000000000000000000000000000002", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now + 3600},
		{Id: 3, Name: "alpha-expired", Key: "00000000000000000000000000000003", Status: common.RedemptionCodeStatusEnabled, ExpiredTime: now - 10},
		{Id: 4, Name: "beta-disabled", Key: "aaaaaaaaaaMiXeDbbbbbbbbbbbbbbbbb", Status: common.RedemptionCodeStatusDisabled, ExpiredTime: 0},
		{Id: 5, Name: "beta-used", Key: "00000000000000000000000000000005", Status: common.RedemptionCodeStatusUsed, ExpiredTime: 0},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	tests := []struct {
		name      string
		keyword   string
		status    string
		startIdx  int
		num       int
		wantTotal int64
		wantIds   []int
	}{
		{
			name:      "no filters returns all rows",
			num:       10,
			wantTotal: 5,
			wantIds:   []int{5, 4, 3, 2, 1},
		},
		{
			name:      "keyword matches name substring ignoring case",
			keyword:   "A-ACT",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{1},
		},
		{
			name:      "keyword matches redemption code substring ignoring case",
			keyword:   "  mIxEd  ",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{4},
		},
		{
			name:      "keyword combines with status and keeps unpaged total",
			keyword:   "alpha",
			status:    "1",
			startIdx:  1,
			num:       1,
			wantTotal: 2,
			wantIds:   []int{1},
		},
		{
			name:      "keyword does not match unrelated substring",
			keyword:   "not-present",
			num:       10,
			wantTotal: 0,
			wantIds:   []int{},
		},
		{
			name:      "numeric keyword matches id",
			keyword:   "4",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{4},
		},
		{
			name:      "enabled status excludes expired rows",
			status:    "1",
			num:       10,
			wantTotal: 2,
			wantIds:   []int{2, 1},
		},
		{
			name:      "expired status returns enabled expired rows",
			status:    "expired",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{3},
		},
		{
			name:      "disabled status",
			status:    "2",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{4},
		},
		{
			name:      "used status",
			status:    "3",
			num:       10,
			wantTotal: 1,
			wantIds:   []int{5},
		},
		{
			name:      "pagination keeps unpaged total",
			startIdx:  1,
			num:       2,
			wantTotal: 5,
			wantIds:   []int{4, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := SearchRedemptions(tt.keyword, tt.status, tt.startIdx, tt.num)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total)
			gotIds := make([]int, 0, len(rows))
			for _, row := range rows {
				gotIds = append(gotIds, row.Id)
			}
			assert.Equal(t, tt.wantIds, gotIds)
		})
	}
}

// TestRedemptionListsAttachCurrentUsername verifies list responses enrich users without persisted snapshots.
func TestRedemptionListsAttachCurrentUsername(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}, &User{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		require.NoError(t, DB.Exec("DELETE FROM users").Error)
	})

	user := User{Username: "lookup-before", Password: "password", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	redemptions := []Redemption{
		{Name: "lookup-found", Key: "10000000000000000000000000000001", Status: common.RedemptionCodeStatusUsed, UsedUserId: user.Id},
		{Name: "lookup-missing", Key: "10000000000000000000000000000002", Status: common.RedemptionCodeStatusUsed, UsedUserId: user.Id + 9999},
	}
	require.NoError(t, DB.Create(&redemptions).Error)

	items, total, err := GetAllRedemptions(0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	assert.Empty(t, items[0].UsedUsername)
	assert.Equal(t, "lookup-before", items[1].UsedUsername)

	require.NoError(t, DB.Model(&user).Update("username", "lookup-after").Error)
	items, total, err = SearchRedemptions("lookup", "", 0, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	assert.Empty(t, items[0].UsedUsername)
	assert.Equal(t, "lookup-after", items[1].UsedUsername)
}

func setupRedeemFixture(t *testing.T, quota int) (userId int, key string) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
	})

	user := &User{Username: "redeem-user", Password: "password", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(user).Error)

	key = "10000000000000000000000000000001"
	redemption := &Redemption{
		Name:        "redeem-test",
		Key:         key,
		Status:      common.RedemptionCodeStatusEnabled,
		Quota:       quota,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(redemption).Error)
	return user.Id, key
}

func TestRedeemCreditsQuotaExactlyOnce(t *testing.T) {
	userId, key := setupRedeemFixture(t, 500)

	result, err := Redeem(key, userId)
	require.NoError(t, err)
	assert.Equal(t, &RedemptionResult{
		Type:  RedemptionTypeQuota,
		Quota: 500,
	}, result)

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)

	var redemption Redemption
	require.NoError(t, DB.First(&redemption, "name = ?", "redeem-test").Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, userId, redemption.UsedUserId)

	// Redeeming the same code again must fail and must not credit quota.
	_, err = Redeem(key, userId)
	require.Error(t, err)
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 500, user.Quota)
}

// Exactly one of several concurrent redeems of the same code may win, and
// quota must be credited exactly once.
func TestRedeemConcurrentSingleSuccess(t *testing.T) {
	userId, key := setupRedeemFixture(t, 300)

	const goroutines = 5
	successes := make([]bool, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			if _, err := Redeem(key, userId); err == nil {
				successes[idx] = true
			}
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	assert.Equal(t, 1, successCount, "exactly one concurrent redeem should succeed")

	var user User
	require.NoError(t, DB.First(&user, "id = ?", userId).Error)
	assert.Equal(t, 300, user.Quota, "quota must be credited exactly once")
}

// TestRedeemCreatesSubscription verifies that a plan code grants the latest plan entitlement without wallet credit.
func TestRedeemCreatesSubscription(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&UserSubscription{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubscriptionOrder{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubscriptionPlan{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&UserSubscription{}).Error)
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubscriptionOrder{}).Error)
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubscriptionPlan{}).Error)
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
	})

	user := &User{
		Username: "subscription-redeem-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    75,
	}
	require.NoError(t, DB.Create(user).Error)

	plan := &SubscriptionPlan{
		Title:              "30-day redemption plan",
		PriceAmount:        9.9,
		Currency:           "USD",
		DurationUnit:       SubscriptionDurationDay,
		DurationValue:      30,
		Enabled:            true,
		TotalAmount:        900,
		QuotaResetPeriod:   SubscriptionResetNever,
		RepeatPurchaseMode: SubscriptionRepeatPurchaseIndependent,
	}
	plan.NormalizeDefaults()
	require.NoError(t, DB.Create(plan).Error)
	// A generated code remains redeemable after an administrator disables its plan.
	require.NoError(t, DB.Model(plan).Update("enabled", false).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	key := "20000000000000000000000000000001"
	require.NoError(t, DB.Create(&Redemption{
		Name:        "subscription-redeem-test",
		Key:         key,
		Status:      common.RedemptionCodeStatusEnabled,
		PlanId:      plan.Id,
		Quota:       0,
		CreatedTime: common.GetTimestamp(),
	}).Error)
	var createdRedemption Redemption
	require.NoError(t, DB.Where("key = ?", key).First(&createdRedemption).Error)
	require.Zero(t, createdRedemption.Quota)

	result, err := Redeem(key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, &RedemptionResult{
		Type:      RedemptionTypeSubscription,
		PlanId:    plan.Id,
		PlanTitle: plan.Title,
	}, result)

	var persistedUser User
	require.NoError(t, DB.First(&persistedUser, "id = ?", user.Id).Error)
	assert.Equal(t, 75, persistedUser.Quota)

	var subscription UserSubscription
	require.NoError(t, DB.Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).First(&subscription).Error)
	assert.Equal(t, SubscriptionSourceRedemption, subscription.Source)
	assert.EqualValues(t, 900, subscription.AmountTotal)
	assert.Equal(t, "active", subscription.Status)

	var redemption Redemption
	require.NoError(t, DB.Where("key = ?", key).First(&redemption).Error)
	assert.Equal(t, common.RedemptionCodeStatusUsed, redemption.Status)
	assert.Equal(t, user.Id, redemption.UsedUserId)

	var orderCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).
		Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).
		Count(&orderCount).Error)
	assert.Zero(t, orderCount)
}

// TestRedeemSubscriptionLimitRollsBackCode verifies that grant failures do not consume a code.
func TestRedeemSubscriptionLimitRollsBackCode(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Redemption{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&UserSubscription{}).Error)
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubscriptionPlan{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Unscoped().Delete(&Redemption{}).Error)
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&UserSubscription{}).Error)
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SubscriptionPlan{}).Error)
		DB.Exec("DELETE FROM users")
		DB.Exec("DELETE FROM logs")
	})

	user := &User{
		Username: "subscription-limit-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Title:              "limited plan",
		PriceAmount:        1,
		Currency:           "USD",
		DurationUnit:       SubscriptionDurationDay,
		DurationValue:      1,
		Enabled:            true,
		MaxPurchasePerUser: 1,
		QuotaResetPeriod:   SubscriptionResetNever,
		RepeatPurchaseMode: SubscriptionRepeatPurchaseIndependent,
	}
	plan.NormalizeDefaults()
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	require.NoError(t, DB.Create(&UserSubscription{
		UserId:          user.Id,
		PlanId:          plan.Id,
		AllocationCount: 1,
		Status:          "expired",
		Source:          "admin",
	}).Error)

	key := "30000000000000000000000000000001"
	require.NoError(t, DB.Create(&Redemption{
		Name:        "subscription-limit-test",
		Key:         key,
		Status:      common.RedemptionCodeStatusEnabled,
		PlanId:      plan.Id,
		CreatedTime: common.GetTimestamp(),
	}).Error)

	_, err := Redeem(key, user.Id)
	require.ErrorIs(t, err, ErrRedeemFailed)

	var redemption Redemption
	require.NoError(t, DB.Where("key = ?", key).First(&redemption).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, redemption.Status)
	assert.Zero(t, redemption.UsedUserId)

	var allocationCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).
		Select("COALESCE(SUM(allocation_count), 0)").
		Scan(&allocationCount).Error)
	assert.EqualValues(t, 1, allocationCount)
}
