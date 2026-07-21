package model

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedRepeatPurchaseUserAndPlan(t *testing.T, userId int, planId int, mode string) *SubscriptionPlan {
	t.Helper()
	user := &User{
		Id:       userId,
		Username: "repeat_user_" + common.GetRandomString(8),
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, DB.Create(user).Error)
	plan := &SubscriptionPlan{
		Id:                 planId,
		Title:              "Repeat Plan",
		PriceAmount:        10,
		Currency:           "USD",
		DurationUnit:       SubscriptionDurationCustom,
		CustomSeconds:      3600,
		Enabled:            true,
		TotalAmount:        100,
		QuotaResetPeriod:   SubscriptionResetNever,
		RepeatPurchaseMode: mode,
	}
	require.NoError(t, DB.Create(plan).Error)
	return plan
}

func applyRepeatPurchase(t *testing.T, userId int, plan *SubscriptionPlan, mode string, enforceLimit bool, source string) *SubscriptionApplyResult {
	t.Helper()
	var result *SubscriptionApplyResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = CreateUserSubscriptionFromPlanTx(tx, userId, plan, SubscriptionApplyOptions{
			Source:               source,
			ApplyMode:            mode,
			EnforcePurchaseLimit: enforceLimit,
		})
		return err
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Subscription)
	return result
}

func getRepeatPurchaseSubscriptions(t *testing.T, userId int, planId int) []UserSubscription {
	t.Helper()
	var subscriptions []UserSubscription
	require.NoError(t, DB.Where("user_id = ? AND plan_id = ?", userId, planId).Order("id asc").Find(&subscriptions).Error)
	return subscriptions
}

func TestResolveSubscriptionApplyMode(t *testing.T) {
	testCases := []struct {
		name       string
		applyMode  string
		planMode   string
		expected   string
		shouldFail bool
	}{
		{name: "empty follows plan", planMode: SubscriptionRepeatPurchaseAddQuota, expected: SubscriptionRepeatPurchaseAddQuota},
		{name: "plan default follows plan", applyMode: SubscriptionApplyModePlanDefault, planMode: SubscriptionRepeatPurchaseExtendTime, expected: SubscriptionRepeatPurchaseExtendTime},
		{name: "override", applyMode: SubscriptionRepeatPurchaseReplace, planMode: SubscriptionRepeatPurchaseIndependent, expected: SubscriptionRepeatPurchaseReplace},
		{name: "max validity", applyMode: SubscriptionRepeatPurchaseMaxValidity, planMode: SubscriptionRepeatPurchaseIndependent, expected: SubscriptionRepeatPurchaseMaxValidity},
		{name: "max validity and add quota", applyMode: SubscriptionRepeatPurchaseMaxValidityAddQuota, planMode: SubscriptionRepeatPurchaseIndependent, expected: SubscriptionRepeatPurchaseMaxValidityAddQuota},
		{name: "extend time and reset quota", applyMode: SubscriptionRepeatPurchaseExtendTimeResetQuota, planMode: SubscriptionRepeatPurchaseIndependent, expected: SubscriptionRepeatPurchaseExtendTimeResetQuota},
		{name: "legacy plan value", applyMode: SubscriptionApplyModePlanDefault, planMode: "", expected: SubscriptionRepeatPurchaseIndependent},
		{name: "invalid override", applyMode: "unknown", planMode: SubscriptionRepeatPurchaseIndependent, shouldFail: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := ResolveSubscriptionApplyMode(testCase.applyMode, testCase.planMode)
			if testCase.shouldFail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, actual)
		})
	}
}

func TestRepeatedSubscriptionModes(t *testing.T) {
	testCases := []struct {
		name              string
		mode              string
		expectedRows      int
		expectedTotal     int64
		expectedUsed      int64
		expectedEndChange int64
		expectedAction    string
	}{
		{name: "independent", mode: SubscriptionRepeatPurchaseIndependent, expectedRows: 2, expectedTotal: 100, expectedUsed: 0, expectedEndChange: 0, expectedAction: SubscriptionApplyActionCreated},
		{name: "extend time", mode: SubscriptionRepeatPurchaseExtendTime, expectedRows: 1, expectedTotal: 100, expectedUsed: 25, expectedEndChange: 3600, expectedAction: SubscriptionApplyActionMerged},
		{name: "add quota", mode: SubscriptionRepeatPurchaseAddQuota, expectedRows: 1, expectedTotal: 200, expectedUsed: 25, expectedEndChange: 0, expectedAction: SubscriptionApplyActionMerged},
		{name: "extend time and add quota", mode: SubscriptionRepeatPurchaseExtendTimeAddQuota, expectedRows: 1, expectedTotal: 200, expectedUsed: 25, expectedEndChange: 3600, expectedAction: SubscriptionApplyActionMerged},
		{name: "max validity", mode: SubscriptionRepeatPurchaseMaxValidity, expectedRows: 1, expectedTotal: 100, expectedUsed: 25, expectedEndChange: 0, expectedAction: SubscriptionApplyActionMerged},
		{name: "max validity and add quota", mode: SubscriptionRepeatPurchaseMaxValidityAddQuota, expectedRows: 1, expectedTotal: 200, expectedUsed: 25, expectedEndChange: 0, expectedAction: SubscriptionApplyActionMerged},
		{name: "extend time and reset quota", mode: SubscriptionRepeatPurchaseExtendTimeResetQuota, expectedRows: 1, expectedTotal: 100, expectedUsed: 0, expectedEndChange: 3600, expectedAction: SubscriptionApplyActionMerged},
		{name: "replace", mode: SubscriptionRepeatPurchaseReplace, expectedRows: 1, expectedTotal: 100, expectedUsed: 0, expectedEndChange: 3500, expectedAction: SubscriptionApplyActionMerged},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncateTables(t)
			userId := 1000 + index
			planId := 2000 + index
			plan := seedRepeatPurchaseUserAndPlan(t, userId, planId, testCase.mode)
			first := applyRepeatPurchase(t, userId, plan, SubscriptionApplyModePlanDefault, true, "order")
			originalEnd := first.Subscription.EndTime

			if testCase.mode != SubscriptionRepeatPurchaseIndependent {
				updates := map[string]interface{}{"amount_used": 25}
				if testCase.mode == SubscriptionRepeatPurchaseReplace {
					updates["start_time"] = first.Subscription.StartTime - 1000
					updates["end_time"] = first.Subscription.StartTime + 100
					originalEnd = first.Subscription.StartTime + 100
				} else if testCase.mode == SubscriptionRepeatPurchaseMaxValidity || testCase.mode == SubscriptionRepeatPurchaseMaxValidityAddQuota {
					originalEnd += 100
					updates["end_time"] = originalEnd
				}
				require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", first.Subscription.Id).Updates(updates).Error)
			}

			second := applyRepeatPurchase(t, userId, plan, SubscriptionApplyModePlanDefault, true, "order")
			subscriptions := getRepeatPurchaseSubscriptions(t, userId, planId)
			require.Len(t, subscriptions, testCase.expectedRows)
			assert.Equal(t, testCase.expectedAction, second.Action)

			if testCase.mode == SubscriptionRepeatPurchaseIndependent {
				assert.EqualValues(t, 1, subscriptions[0].AllocationCount)
				assert.EqualValues(t, 1, subscriptions[1].AllocationCount)
			} else {
				actual := subscriptions[0]
				assert.Equal(t, testCase.expectedTotal, actual.AmountTotal)
				assert.Equal(t, testCase.expectedUsed, actual.AmountUsed)
				assert.Equal(t, originalEnd+testCase.expectedEndChange, actual.EndTime)
				assert.EqualValues(t, 2, actual.AllocationCount)
			}
			count, err := CountUserSubscriptionsByPlan(userId, planId)
			require.NoError(t, err)
			assert.EqualValues(t, 2, count)
		})
	}
}

func TestRepeatedSubscriptionMaxValidityModes(t *testing.T) {
	testCases := []struct {
		name          string
		mode          string
		currentEnd    int64
		incomingEnd   int64
		expectedEnd   int64
		expectedTotal int64
	}{
		{name: "keep later current end", mode: SubscriptionRepeatPurchaseMaxValidity, currentEnd: 7200, incomingEnd: 3600, expectedEnd: 7200, expectedTotal: 100},
		{name: "use later incoming end", mode: SubscriptionRepeatPurchaseMaxValidity, currentEnd: 1800, incomingEnd: 3600, expectedEnd: 3600, expectedTotal: 100},
		{name: "keep equal end", mode: SubscriptionRepeatPurchaseMaxValidity, currentEnd: 3600, incomingEnd: 3600, expectedEnd: 3600, expectedTotal: 100},
		{name: "add quota with later current end", mode: SubscriptionRepeatPurchaseMaxValidityAddQuota, currentEnd: 7200, incomingEnd: 3600, expectedEnd: 7200, expectedTotal: 200},
		{name: "add quota with later incoming end", mode: SubscriptionRepeatPurchaseMaxValidityAddQuota, currentEnd: 1800, incomingEnd: 3600, expectedEnd: 3600, expectedTotal: 200},
		{name: "add quota with equal end", mode: SubscriptionRepeatPurchaseMaxValidityAddQuota, currentEnd: 3600, incomingEnd: 3600, expectedEnd: 3600, expectedTotal: 200},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncateTables(t)
			userId := 2100 + index
			plan := seedRepeatPurchaseUserAndPlan(t, userId, 2200+index, testCase.mode)
			now := GetDBTimestamp()
			target := &UserSubscription{
				UserId: userId, PlanId: plan.Id, AmountTotal: 100, AmountUsed: 25,
				StartTime: now - 100, EndTime: now + testCase.currentEnd, Status: "active", AllocationCount: 1,
			}
			require.NoError(t, DB.Create(target).Error)
			incoming := &UserSubscription{
				UserId: userId, PlanId: plan.Id, AmountTotal: 100,
				StartTime: now, EndTime: now + testCase.incomingEnd, Status: "active", AllocationCount: 1,
			}

			var result *SubscriptionApplyResult
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				var err error
				result, err = mergeRepeatedUserSubscriptionTx(tx, plan, incoming, testCase.mode)
				return err
			}))

			require.NotNil(t, result)
			assert.Equal(t, now+testCase.expectedEnd, result.Subscription.EndTime)
			assert.Equal(t, testCase.expectedTotal, result.Subscription.AmountTotal)
			assert.EqualValues(t, 25, result.Subscription.AmountUsed)
			assert.EqualValues(t, 2, result.Subscription.AllocationCount)
		})
	}
}

func TestRepeatedSubscriptionExtendTimeResetQuotaUsesLatestPlanQuota(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 2801, 2802, SubscriptionRepeatPurchaseExtendTimeResetQuota)
	first := applyRepeatPurchase(t, 2801, plan, SubscriptionApplyModePlanDefault, true, "order")
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", first.Subscription.Id).Update("amount_used", 60).Error)
	plan.TotalAmount = 250

	second := applyRepeatPurchase(t, 2801, plan, SubscriptionApplyModePlanDefault, true, "order")

	assert.Equal(t, first.Subscription.EndTime+3600, second.Subscription.EndTime)
	assert.EqualValues(t, 250, second.Subscription.AmountTotal)
	assert.Zero(t, second.Subscription.AmountUsed)
	assert.EqualValues(t, 2, second.Subscription.AllocationCount)
}

func TestRepeatedSubscriptionUsesLatestActiveTargetAndKeepsHistory(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 3001, 3002, SubscriptionRepeatPurchaseAddQuota)
	now := GetDBTimestamp()
	rows := []UserSubscription{
		{Id: 3101, UserId: 3001, PlanId: plan.Id, AmountTotal: 100, StartTime: now - 100, EndTime: now + 600, Status: "active", AllocationCount: 1},
		{Id: 3102, UserId: 3001, PlanId: plan.Id, AmountTotal: 100, StartTime: now - 100, EndTime: now + 1200, Status: "active", AllocationCount: 1},
		{Id: 3103, UserId: 3001, PlanId: plan.Id, AmountTotal: 100, StartTime: now - 2000, EndTime: now - 1, Status: "active", AllocationCount: 1},
	}
	require.NoError(t, DB.Create(&rows).Error)

	result := applyRepeatPurchase(t, 3001, plan, SubscriptionApplyModePlanDefault, true, "order")

	assert.Equal(t, 3102, result.Subscription.Id)
	actual := getRepeatPurchaseSubscriptions(t, 3001, plan.Id)
	require.Len(t, actual, 3)
	assert.EqualValues(t, 100, actual[0].AmountTotal)
	assert.EqualValues(t, 200, actual[1].AmountTotal)
	assert.EqualValues(t, 100, actual[2].AmountTotal)
	assert.EqualValues(t, 2, actual[1].AllocationCount)
}

func TestRepeatedSubscriptionCreatesWhenOnlyExpiredTargetExists(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 3201, 3202, SubscriptionRepeatPurchaseAddQuota)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		Id: 3203, UserId: 3201, PlanId: plan.Id, AmountTotal: 100, StartTime: now - 7200,
		EndTime: now - 1, Status: "active", AllocationCount: 1,
	}).Error)

	result := applyRepeatPurchase(t, 3201, plan, SubscriptionApplyModePlanDefault, true, "order")

	assert.Equal(t, SubscriptionApplyActionCreated, result.Action)
	assert.Len(t, getRepeatPurchaseSubscriptions(t, 3201, plan.Id), 2)
}

func TestRepeatedSubscriptionQuotaBoundaries(t *testing.T) {
	for index, mode := range []string{SubscriptionRepeatPurchaseAddQuota, SubscriptionRepeatPurchaseMaxValidityAddQuota} {
		t.Run(mode+" unlimited stays unlimited", func(t *testing.T) {
			truncateTables(t)
			userId := 3301 + index
			plan := seedRepeatPurchaseUserAndPlan(t, userId, 3303+index, mode)
			first := applyRepeatPurchase(t, userId, plan, SubscriptionApplyModePlanDefault, true, "order")
			require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", first.Subscription.Id).Update("amount_total", 0).Error)

			second := applyRepeatPurchase(t, userId, plan, SubscriptionApplyModePlanDefault, true, "order")

			assert.Zero(t, second.Subscription.AmountTotal)
			assert.EqualValues(t, 2, second.Subscription.AllocationCount)
		})

		t.Run(mode+" overflow rolls back", func(t *testing.T) {
			truncateTables(t)
			userId := 3401 + index
			plan := seedRepeatPurchaseUserAndPlan(t, userId, 3403+index, mode)
			first := applyRepeatPurchase(t, userId, plan, SubscriptionApplyModePlanDefault, true, "order")
			require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", first.Subscription.Id).Update("amount_total", int64(math.MaxInt64)).Error)

			err := DB.Transaction(func(tx *gorm.DB) error {
				_, err := CreateUserSubscriptionFromPlanTx(tx, userId, plan, SubscriptionApplyOptions{
					Source: "order", ApplyMode: SubscriptionApplyModePlanDefault, EnforcePurchaseLimit: true,
				})
				return err
			})

			require.ErrorContains(t, err, "quota overflow")
			actual := getRepeatPurchaseSubscriptions(t, userId, plan.Id)
			require.Len(t, actual, 1)
			assert.EqualValues(t, 1, actual[0].AllocationCount)
			assert.Equal(t, int64(math.MaxInt64), actual[0].AmountTotal)
		})
	}
}

func TestAdminSubscriptionGrantBypassesLimitAndCanOverrideMode(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 3501, 3502, SubscriptionRepeatPurchaseAddQuota)
	plan.MaxPurchasePerUser = 1
	require.NoError(t, DB.Model(plan).Update("max_purchase_per_user", 1).Error)

	first := applyRepeatPurchase(t, 3501, plan, SubscriptionApplyModePlanDefault, true, "order")
	assert.Equal(t, SubscriptionApplyActionCreated, first.Action)

	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := CreateUserSubscriptionFromPlanTx(tx, 3501, plan, SubscriptionApplyOptions{
			Source: "order", ApplyMode: SubscriptionApplyModePlanDefault, EnforcePurchaseLimit: true,
		})
		return err
	})
	require.ErrorContains(t, err, "购买上限")

	adminMerged, err := AdminBindSubscription(3501, plan.Id, SubscriptionApplyModePlanDefault)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionApplyActionMerged, adminMerged.Action)
	assert.EqualValues(t, 200, adminMerged.Subscription.AmountTotal)
	assert.EqualValues(t, 2, adminMerged.Subscription.AllocationCount)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", adminMerged.Subscription.Id).Update("amount_used", 40).Error)

	adminReset, err := AdminBindSubscription(3501, plan.Id, SubscriptionRepeatPurchaseExtendTimeResetQuota)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionApplyActionMerged, adminReset.Action)
	assert.Equal(t, SubscriptionRepeatPurchaseExtendTimeResetQuota, adminReset.AppliedMode)
	assert.EqualValues(t, 100, adminReset.Subscription.AmountTotal)
	assert.Zero(t, adminReset.Subscription.AmountUsed)
	assert.EqualValues(t, 3, adminReset.Subscription.AllocationCount)

	adminIndependent, err := AdminBindSubscription(3501, plan.Id, SubscriptionRepeatPurchaseIndependent)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionApplyActionCreated, adminIndependent.Action)
	assert.Len(t, getRepeatPurchaseSubscriptions(t, 3501, plan.Id), 2)

	count, err := CountUserSubscriptionsByPlan(3501, plan.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 4, count)
}

func TestRepeatedSubscriptionReopensResetScheduleAndPreservesPreviousGroup(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 3601, 3602, SubscriptionRepeatPurchaseExtendTime)
	plan.CustomSeconds = 60
	plan.QuotaResetPeriod = SubscriptionResetCustom
	plan.QuotaResetCustomSeconds = 120
	plan.UpgradeGroup = "vip"
	require.NoError(t, DB.Model(plan).Updates(map[string]interface{}{
		"custom_seconds": 60, "quota_reset_period": SubscriptionResetCustom,
		"quota_reset_custom_seconds": 120, "upgrade_group": "vip",
	}).Error)

	first := applyRepeatPurchase(t, 3601, plan, SubscriptionApplyModePlanDefault, true, "order")
	assert.Zero(t, first.Subscription.NextResetTime)
	assert.Equal(t, "default", first.Subscription.PrevUserGroup)

	second := applyRepeatPurchase(t, 3601, plan, SubscriptionApplyModePlanDefault, true, "order")

	assert.Equal(t, first.Subscription.EndTime+60, second.Subscription.EndTime)
	assert.Equal(t, first.Subscription.StartTime+120, second.Subscription.NextResetTime)
	assert.Equal(t, "default", second.Subscription.PrevUserGroup)
}

func TestExtendTimeResetQuotaKeepsOrReopensResetSchedule(t *testing.T) {
	t.Run("keeps active reset schedule", func(t *testing.T) {
		truncateTables(t)
		plan := seedRepeatPurchaseUserAndPlan(t, 3651, 3652, SubscriptionRepeatPurchaseExtendTimeResetQuota)
		plan.QuotaResetPeriod = SubscriptionResetCustom
		plan.QuotaResetCustomSeconds = 120
		require.NoError(t, DB.Model(plan).Updates(map[string]interface{}{
			"quota_reset_period": SubscriptionResetCustom, "quota_reset_custom_seconds": 120,
		}).Error)

		first := applyRepeatPurchase(t, 3651, plan, SubscriptionApplyModePlanDefault, true, "order")
		require.NotZero(t, first.Subscription.NextResetTime)
		require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", first.Subscription.Id).Update("amount_used", 50).Error)

		second := applyRepeatPurchase(t, 3651, plan, SubscriptionApplyModePlanDefault, true, "order")

		assert.Equal(t, first.Subscription.LastResetTime, second.Subscription.LastResetTime)
		assert.Equal(t, first.Subscription.NextResetTime, second.Subscription.NextResetTime)
		assert.Zero(t, second.Subscription.AmountUsed)
	})

	t.Run("reopens stopped reset schedule", func(t *testing.T) {
		truncateTables(t)
		plan := seedRepeatPurchaseUserAndPlan(t, 3661, 3662, SubscriptionRepeatPurchaseExtendTimeResetQuota)
		plan.CustomSeconds = 60
		plan.QuotaResetPeriod = SubscriptionResetCustom
		plan.QuotaResetCustomSeconds = 120
		require.NoError(t, DB.Model(plan).Updates(map[string]interface{}{
			"custom_seconds": 60, "quota_reset_period": SubscriptionResetCustom,
			"quota_reset_custom_seconds": 120,
		}).Error)

		first := applyRepeatPurchase(t, 3661, plan, SubscriptionApplyModePlanDefault, true, "order")
		assert.Zero(t, first.Subscription.NextResetTime)

		second := applyRepeatPurchase(t, 3661, plan, SubscriptionApplyModePlanDefault, true, "order")

		assert.Equal(t, first.Subscription.EndTime+60, second.Subscription.EndTime)
		assert.Equal(t, first.Subscription.StartTime+120, second.Subscription.NextResetTime)
	})
}

func TestMaxValidityReopensResetScheduleOnlyWhenExtended(t *testing.T) {
	testCases := []struct {
		name        string
		currentEnd  int64
		incomingEnd int64
		shouldOpen  bool
	}{
		{name: "reopens after extension", currentEnd: 60, incomingEnd: 3600, shouldOpen: true},
		{name: "stays stopped without extension", currentEnd: 3600, incomingEnd: 1800, shouldOpen: false},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			truncateTables(t)
			userId := 3671 + index
			plan := seedRepeatPurchaseUserAndPlan(t, userId, 3673+index, SubscriptionRepeatPurchaseMaxValidity)
			plan.QuotaResetPeriod = SubscriptionResetCustom
			plan.QuotaResetCustomSeconds = 120
			now := GetDBTimestamp()
			target := &UserSubscription{
				UserId: userId, PlanId: plan.Id, AmountTotal: 100,
				StartTime: now - 100, EndTime: now + testCase.currentEnd, Status: "active", AllocationCount: 1,
			}
			require.NoError(t, DB.Create(target).Error)
			incoming := &UserSubscription{
				UserId: userId, PlanId: plan.Id, AmountTotal: 100,
				StartTime: now, EndTime: now + testCase.incomingEnd, Status: "active", AllocationCount: 1,
			}

			var result *SubscriptionApplyResult
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				var err error
				result, err = mergeRepeatedUserSubscriptionTx(tx, plan, incoming, SubscriptionRepeatPurchaseMaxValidity)
				return err
			}))

			require.NotNil(t, result)
			if testCase.shouldOpen {
				assert.NotZero(t, result.Subscription.NextResetTime)
			} else {
				assert.Zero(t, result.Subscription.NextResetTime)
			}
		})
	}
}

func TestMergedQuotaCanCoverOneLargeRequest(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 3701, 3702, SubscriptionRepeatPurchaseAddQuota)
	applyRepeatPurchase(t, 3701, plan, SubscriptionApplyModePlanDefault, true, "order")
	applyRepeatPurchase(t, 3701, plan, SubscriptionApplyModePlanDefault, true, "order")

	result, err := PreConsumeUserSubscription("repeat-large-request", 3701, "test-model", 0, 150)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.EqualValues(t, 150, result.PreConsumed)
	assert.EqualValues(t, 200, result.AmountTotal)
	assert.EqualValues(t, 150, result.AmountUsedAfter)
}

func TestSubscriptionPaymentCompletionAppliesOncePerOrder(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 3801, 3802, SubscriptionRepeatPurchaseAddQuota)
	insertSubscriptionOrderForPaymentGuardTest(t, "repeat-order-1", 3801, plan.Id, PaymentProviderStripe)
	insertSubscriptionOrderForPaymentGuardTest(t, "repeat-order-2", 3801, plan.Id, PaymentProviderStripe)

	require.NoError(t, CompleteSubscriptionOrder("repeat-order-1", "", PaymentProviderStripe, ""))
	require.NoError(t, CompleteSubscriptionOrder("repeat-order-1", "", PaymentProviderStripe, ""))
	count, err := CountUserSubscriptionsByPlan(3801, plan.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)

	require.NoError(t, CompleteSubscriptionOrder("repeat-order-2", "", PaymentProviderStripe, ""))
	subscriptions := getRepeatPurchaseSubscriptions(t, 3801, plan.Id)
	require.Len(t, subscriptions, 1)
	assert.EqualValues(t, 200, subscriptions[0].AmountTotal)
	assert.EqualValues(t, 2, subscriptions[0].AllocationCount)
}

func TestBalancePurchaseUsesPlanRepeatMode(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 3901, 3902, SubscriptionRepeatPurchaseExtendTime)
	plan.PriceAmount = 0
	require.NoError(t, DB.Model(plan).Update("price_amount", 0).Error)

	require.NoError(t, PurchaseSubscriptionWithBalance(3901, plan.Id))
	first := getRepeatPurchaseSubscriptions(t, 3901, plan.Id)
	require.Len(t, first, 1)
	require.NoError(t, PurchaseSubscriptionWithBalance(3901, plan.Id))

	second := getRepeatPurchaseSubscriptions(t, 3901, plan.Id)
	require.Len(t, second, 1)
	assert.Equal(t, first[0].EndTime+3600, second[0].EndTime)
	assert.EqualValues(t, 2, second[0].AllocationCount)
}

func TestSubscriptionAllocationBackfillAndLegacyCount(t *testing.T) {
	truncateTables(t)
	plan := seedRepeatPurchaseUserAndPlan(t, 4001, 4002, SubscriptionRepeatPurchaseIndependent)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		Id: 4003, UserId: 4001, PlanId: plan.Id, AmountTotal: 100,
		StartTime: now, EndTime: now + 3600, Status: "active", AllocationCount: 1,
	}).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", 4003).Update("allocation_count", 0).Error)

	count, err := CountUserSubscriptionsByPlan(4001, plan.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	require.NoError(t, backfillSubscriptionAllocationCount())

	actual := getRepeatPurchaseSubscriptions(t, 4001, plan.Id)
	require.Len(t, actual, 1)
	assert.EqualValues(t, 1, actual[0].AllocationCount)
}
