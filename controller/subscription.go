package controller

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- Shared types ----

type SubscriptionPlanDTO struct {
	Plan model.SubscriptionPlan `json:"plan"`
}

// PublicSubscriptionPlanDTO contains the plan fields required by ordinary purchase flows.
type PublicSubscriptionPlanDTO struct {
	Plan PublicSubscriptionPlan `json:"plan"`
}

// PublicSubscriptionPlan omits provider product IDs and database metadata.
type PublicSubscriptionPlan struct {
	Id                      int              `json:"id"`
	Title                   string           `json:"title"`
	Subtitle                string           `json:"subtitle"`
	PriceAmount             float64          `json:"price_amount"`
	Currency                string           `json:"currency"`
	DurationUnit            string           `json:"duration_unit"`
	DurationValue           int              `json:"duration_value"`
	CustomSeconds           int64            `json:"custom_seconds"`
	AllowBalancePay         bool             `json:"allow_balance_pay"`
	AllowWalletOverflow     bool             `json:"allow_wallet_overflow"`
	MaxPurchasePerUser      int              `json:"max_purchase_per_user"`
	RepeatPurchaseMode      string           `json:"repeat_purchase_mode"`
	EntitlementGroup        string           `json:"entitlement_group"`
	GrantGroups             model.GroupNames `json:"grant_groups"`
	UpgradeGroup            string           `json:"upgrade_group"`
	DowngradeGroup          string           `json:"downgrade_group"`
	TotalAmount             int64            `json:"total_amount"`
	QuotaResetPeriod        string           `json:"quota_reset_period"`
	QuotaResetCustomSeconds int64            `json:"quota_reset_custom_seconds"`
	AvailablePaymentMethods []string         `json:"available_payment_methods"`
}

type BillingPreferenceRequest struct {
	BillingPreference string `json:"billing_preference"`
}

type SubscriptionBalancePayRequest struct {
	PlanId int `json:"plan_id"`
}

// normalizeSubscriptionPlanGroups validates mutually exclusive group grant modes.
func normalizeSubscriptionPlanGroups(plan *model.SubscriptionPlan) error {
	plan.EntitlementGroup = strings.TrimSpace(plan.EntitlementGroup)
	plan.UpgradeGroup = strings.TrimSpace(plan.UpgradeGroup)
	plan.DowngradeGroup = strings.TrimSpace(plan.DowngradeGroup)
	plan.GrantGroups = model.NormalizeGroupNames(plan.GrantGroups)
	groups := ratio_setting.GetGroupRatioCopy()
	if plan.EntitlementGroup != "" {
		if _, ok := groups[plan.EntitlementGroup]; !ok {
			return fmt.Errorf("权益分组不存在")
		}
		if plan.UpgradeGroup != "" || plan.DowngradeGroup != "" {
			return fmt.Errorf("权益分组不能与升级或降级分组同时配置")
		}
	}
	if plan.UpgradeGroup != "" {
		if _, ok := groups[plan.UpgradeGroup]; !ok {
			return fmt.Errorf("升级分组不存在")
		}
	}
	if plan.DowngradeGroup != "" {
		if _, ok := groups[plan.DowngradeGroup]; !ok {
			return fmt.Errorf("降级分组不存在")
		}
	}
	for _, group := range plan.GrantGroups {
		if _, ok := groups[group]; !ok {
			return fmt.Errorf("权益分组 %s 不存在", group)
		}
		if group == plan.UpgradeGroup {
			return fmt.Errorf("升级分组 %s 已自动可用，请从额外权益分组中移除", group)
		}
	}
	return nil
}

// ---- User APIs ----

func GetSubscriptionPlans(c *gin.Context) {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiSuccess(c, []PublicSubscriptionPlanDTO{})
		return
	}

	var plans []model.SubscriptionPlan
	if err := model.DB.Where("enabled = ?", true).Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]PublicSubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		p.NormalizeDefaults()
		result = append(result, publicSubscriptionPlanDTO(p))
	}
	common.ApiSuccess(c, result)
}

func GetSubscriptionSelf(c *gin.Context) {
	userId := c.GetInt("id")
	settingMap, _ := model.GetUserSetting(userId, false)
	pref := common.NormalizeBillingPreference(settingMap.BillingPreference)

	// Get all subscriptions (including expired)
	allSubscriptions, err := model.GetAllPublicUserSubscriptions(userId)
	if err != nil {
		allSubscriptions = []model.PublicSubscriptionSummary{}
	}

	// Get active subscriptions for backward compatibility
	activeSubscriptions, err := model.GetAllActivePublicUserSubscriptions(userId)
	if err != nil {
		activeSubscriptions = []model.PublicSubscriptionSummary{}
	}

	common.ApiSuccess(c, gin.H{
		"billing_preference": pref,
		"subscriptions":      activeSubscriptions, // all active subscriptions
		"all_subscriptions":  allSubscriptions,    // all subscriptions including expired
	})
}

// publicSubscriptionPlanDTO maps internal payment configuration to public capabilities.
func publicSubscriptionPlanDTO(plan model.SubscriptionPlan) PublicSubscriptionPlanDTO {
	methods := make([]string, 0, 4)
	if plan.AllowBalancePay != nil && *plan.AllowBalancePay {
		methods = append(methods, model.PaymentProviderBalance)
	}
	if strings.TrimSpace(plan.StripePriceId) != "" {
		methods = append(methods, model.PaymentProviderStripe)
	}
	if strings.TrimSpace(plan.CreemProductId) != "" {
		methods = append(methods, model.PaymentProviderCreem)
	}
	if strings.TrimSpace(plan.WaffoPancakeProductId) != "" {
		methods = append(methods, model.PaymentProviderWaffoPancake)
	}
	allowBalancePay := plan.AllowBalancePay != nil && *plan.AllowBalancePay
	allowWalletOverflow := plan.AllowWalletOverflow != nil && *plan.AllowWalletOverflow
	return PublicSubscriptionPlanDTO{Plan: PublicSubscriptionPlan{
		Id:                      plan.Id,
		Title:                   plan.Title,
		Subtitle:                plan.Subtitle,
		PriceAmount:             plan.PriceAmount,
		Currency:                plan.Currency,
		DurationUnit:            plan.DurationUnit,
		DurationValue:           plan.DurationValue,
		CustomSeconds:           plan.CustomSeconds,
		AllowBalancePay:         allowBalancePay,
		AllowWalletOverflow:     allowWalletOverflow,
		MaxPurchasePerUser:      plan.MaxPurchasePerUser,
		RepeatPurchaseMode:      plan.RepeatPurchaseMode,
		EntitlementGroup:        plan.EntitlementGroup,
		GrantGroups:             plan.GrantGroups,
		UpgradeGroup:            plan.UpgradeGroup,
		DowngradeGroup:          plan.DowngradeGroup,
		TotalAmount:             plan.TotalAmount,
		QuotaResetPeriod:        plan.QuotaResetPeriod,
		QuotaResetCustomSeconds: plan.QuotaResetCustomSeconds,
		AvailablePaymentMethods: methods,
	}}
}

func UpdateSubscriptionPreference(c *gin.Context) {
	userId := c.GetInt("id")
	var req BillingPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	pref := common.NormalizeBillingPreference(req.BillingPreference)

	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	current := user.GetSetting()
	current.BillingPreference = pref
	if err := model.UpdateUserSetting(user.Id, current); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"billing_preference": pref})
}

func SubscriptionRequestBalancePay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	userId := c.GetInt("id")
	var req SubscriptionBalancePayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	if err := model.PurchaseSubscriptionWithBalance(userId, req.PlanId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// ---- Admin APIs ----

func AdminListSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	if err := model.DB.Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		p.NormalizeDefaults()
		result = append(result, SubscriptionPlanDTO{
			Plan: p,
		})
	}
	common.ApiSuccess(c, result)
}

type AdminUpsertSubscriptionPlanRequest struct {
	Plan model.SubscriptionPlan `json:"plan"`
}

func AdminCreateSubscriptionPlan(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	req.Plan.Id = 0
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorMsg(c, "套餐标题不能为空")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorMsg(c, "价格不能为负数")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorMsg(c, "价格不能超过9999")
		return
	}
	if req.Plan.Currency == "" {
		req.Plan.Currency = "USD"
	}
	req.Plan.Currency = "USD"
	if req.Plan.AllowBalancePay == nil {
		req.Plan.AllowBalancePay = common.GetPointer(true)
	}
	if req.Plan.AllowWalletOverflow == nil {
		req.Plan.AllowWalletOverflow = common.GetPointer(true)
	}
	if req.Plan.DurationUnit == "" {
		req.Plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if req.Plan.DurationValue <= 0 && req.Plan.DurationUnit != model.SubscriptionDurationCustom {
		req.Plan.DurationValue = 1
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorMsg(c, "购买上限不能为负数")
		return
	}
	req.Plan.RepeatPurchaseMode = strings.TrimSpace(req.Plan.RepeatPurchaseMode)
	if req.Plan.RepeatPurchaseMode == "" {
		req.Plan.RepeatPurchaseMode = model.SubscriptionRepeatPurchaseIndependent
	}
	if !model.IsValidSubscriptionRepeatPurchaseMode(req.Plan.RepeatPurchaseMode) {
		common.ApiErrorMsg(c, "重复购买处理方式无效")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorMsg(c, "总额度不能为负数")
		return
	}
	if err := normalizeSubscriptionPlanGroups(&req.Plan); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	req.Plan.QuotaResetPeriod = model.NormalizeResetPeriod(req.Plan.QuotaResetPeriod)
	if req.Plan.QuotaResetPeriod == model.SubscriptionResetCustom && req.Plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorMsg(c, "自定义重置周期需大于0秒")
		return
	}
	err := model.DB.Create(&req.Plan).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(req.Plan.Id)
	common.ApiSuccess(c, req.Plan)
}

func AdminUpdateSubscriptionPlan(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	grantGroupsProvided := req.Plan.GrantGroups != nil
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorMsg(c, "套餐标题不能为空")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorMsg(c, "价格不能为负数")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorMsg(c, "价格不能超过9999")
		return
	}
	req.Plan.Id = id
	if req.Plan.Currency == "" {
		req.Plan.Currency = "USD"
	}
	req.Plan.Currency = "USD"
	if req.Plan.DurationUnit == "" {
		req.Plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if req.Plan.DurationValue <= 0 && req.Plan.DurationUnit != model.SubscriptionDurationCustom {
		req.Plan.DurationValue = 1
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorMsg(c, "购买上限不能为负数")
		return
	}
	req.Plan.RepeatPurchaseMode = strings.TrimSpace(req.Plan.RepeatPurchaseMode)
	if req.Plan.RepeatPurchaseMode == "" {
		req.Plan.RepeatPurchaseMode = model.SubscriptionRepeatPurchaseIndependent
	}
	if !model.IsValidSubscriptionRepeatPurchaseMode(req.Plan.RepeatPurchaseMode) {
		common.ApiErrorMsg(c, "重复购买处理方式无效")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorMsg(c, "总额度不能为负数")
		return
	}
	if err := normalizeSubscriptionPlanGroups(&req.Plan); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	req.Plan.QuotaResetPeriod = model.NormalizeResetPeriod(req.Plan.QuotaResetPeriod)
	if req.Plan.QuotaResetPeriod == model.SubscriptionResetCustom && req.Plan.QuotaResetCustomSeconds <= 0 {
		common.ApiErrorMsg(c, "自定义重置周期需大于0秒")
		return
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// update plan (allow zero values updates with map)
		updateMap := map[string]interface{}{
			"title":                      req.Plan.Title,
			"subtitle":                   req.Plan.Subtitle,
			"price_amount":               req.Plan.PriceAmount,
			"currency":                   req.Plan.Currency,
			"duration_unit":              req.Plan.DurationUnit,
			"duration_value":             req.Plan.DurationValue,
			"custom_seconds":             req.Plan.CustomSeconds,
			"enabled":                    req.Plan.Enabled,
			"sort_order":                 req.Plan.SortOrder,
			"stripe_price_id":            req.Plan.StripePriceId,
			"creem_product_id":           req.Plan.CreemProductId,
			"waffo_pancake_product_id":   req.Plan.WaffoPancakeProductId,
			"max_purchase_per_user":      req.Plan.MaxPurchasePerUser,
			"repeat_purchase_mode":       req.Plan.RepeatPurchaseMode,
			"total_amount":               req.Plan.TotalAmount,
			"entitlement_group":          req.Plan.EntitlementGroup,
			"upgrade_group":              req.Plan.UpgradeGroup,
			"downgrade_group":            req.Plan.DowngradeGroup,
			"quota_reset_period":         req.Plan.QuotaResetPeriod,
			"quota_reset_custom_seconds": req.Plan.QuotaResetCustomSeconds,
			"updated_at":                 common.GetTimestamp(),
		}
		if req.Plan.AllowBalancePay != nil {
			updateMap["allow_balance_pay"] = *req.Plan.AllowBalancePay
		}
		if req.Plan.AllowWalletOverflow != nil {
			updateMap["allow_wallet_overflow"] = *req.Plan.AllowWalletOverflow
		}
		if grantGroupsProvided {
			updateMap["grant_groups"] = req.Plan.GrantGroups
		}
		if err := tx.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Updates(updateMap).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminUpdateSubscriptionPlanStatusRequest struct {
	Enabled *bool `json:"enabled"`
}

func AdminUpdateSubscriptionPlanStatus(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req AdminUpdateSubscriptionPlanStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if err := model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Update("enabled", *req.Enabled).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminBindSubscriptionRequest struct {
	UserId    int    `json:"user_id"`
	PlanId    int    `json:"plan_id"`
	ApplyMode string `json:"apply_mode"`
}

// subscriptionAuditSnapshot returns the entitlement fields required by management audit logs.
func subscriptionAuditSnapshot(sub *model.UserSubscription) map[string]interface{} {
	if sub == nil {
		return nil
	}
	return map[string]interface{}{
		"subscription_id":   sub.Id,
		"status":            sub.Status,
		"start_time":        sub.StartTime,
		"end_time":          sub.EndTime,
		"amount_total":      sub.AmountTotal,
		"amount_used":       sub.AmountUsed,
		"next_reset_time":   sub.NextResetTime,
		"allocation_count":  sub.AllocationCount,
		"entitlement_group": sub.EntitlementGroup,
		"grant_groups":      sub.GrantGroups,
	}
}

// handleAdminSubscriptionApply applies one admin grant and records both user and management audit logs.
func handleAdminSubscriptionApply(c *gin.Context, userId int, planId int, applyMode string) {
	result, err := model.AdminBindSubscription(userId, planId, applyMode)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if result == nil || result.Subscription == nil {
		common.ApiErrorMsg(c, "订阅分配结果为空")
		return
	}
	actionLabel := "创建"
	if result.Action == model.SubscriptionApplyActionMerged {
		actionLabel = "合并"
	}
	message := fmt.Sprintf("已%s订阅 %s，累计分配 %d 次", actionLabel, result.PlanTitle, result.Subscription.AllocationCount)
	adminInfo := auditOperatorInfo(c)
	model.RecordLogWithAdminInfo(userId, model.LogTypeManage, message, adminInfo)
	recordManageAuditFor(c, userId, "subscription.user_grant", map[string]interface{}{
		"target_user_id": userId,
		"plan_id":        planId,
		"plan_title":     result.PlanTitle,
		"action":         result.Action,
		"applied_mode":   result.AppliedMode,
		"before":         subscriptionAuditSnapshot(result.Before),
		"after":          subscriptionAuditSnapshot(result.Subscription),
	})
	common.ApiSuccess(c, gin.H{
		"message":      message,
		"action":       result.Action,
		"applied_mode": result.AppliedMode,
		"subscription": result.Subscription,
	})
}

func AdminBindSubscription(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req AdminBindSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	handleAdminSubscriptionApply(c, req.UserId, req.PlanId, req.ApplyMode)
}

// ---- Admin: user subscription management ----

func AdminListUserSubscriptions(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	subs, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, subs)
}

type AdminCreateUserSubscriptionRequest struct {
	PlanId    int    `json:"plan_id"`
	ApplyMode string `json:"apply_mode"`
}

type AdminUpdateUserSubscriptionRequest struct {
	EndTime     *int64 `json:"end_time"`
	AmountUsed  *int64 `json:"amount_used"`
	AmountTotal *int64 `json:"amount_total"`
}

type AdminResetSubscriptionRequest struct {
	PlanId           int   `json:"plan_id"`
	AdvanceResetTime *bool `json:"advance_reset_time"`
}

func resolveAdvanceResetTime(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func recordSubscriptionResetUserLogs(result *model.SubscriptionResetResult, adminInfo map[string]interface{}) {
	if result == nil || result.ResetCount == 0 {
		return
	}
	content := fmt.Sprintf("管理员重置订阅套餐 %s（ID: %d）额度", result.PlanTitle, result.PlanId)
	for _, userId := range result.AffectedUserIds {
		model.RecordLogWithAdminInfo(userId, model.LogTypeManage, content, adminInfo)
	}
}

// AdminCreateUserSubscription creates a new user subscription from a plan (no payment).
func AdminCreateUserSubscription(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	var req AdminCreateUserSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	handleAdminSubscriptionApply(c, userId, req.PlanId, req.ApplyMode)
}

// AdminUpdateUserSubscription updates one user subscription and records its before/after state.
func AdminUpdateUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	var req AdminUpdateUserSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	result, err := model.AdminUpdateUserSubscription(subId, model.UserSubscriptionUpdate{
		EndTime:     req.EndTime,
		AmountUsed:  req.AmountUsed,
		AmountTotal: req.AmountTotal,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}

	message := fmt.Sprintf("管理员修改订阅（ID: %d）", subId)
	if result.GroupChanged != "" {
		message += fmt.Sprintf("，用户分组已更新为 %s", result.GroupChanged)
	}
	adminInfo := auditOperatorInfo(c)
	userId := result.Subscription.UserId
	model.RecordLogWithAdminInfo(userId, model.LogTypeManage, message, adminInfo)
	recordManageAuditFor(c, userId, "subscription.user_update", map[string]interface{}{
		"target_user_id":  userId,
		"subscription_id": subId,
		"before":          subscriptionAuditSnapshot(result.Before),
		"after":           subscriptionAuditSnapshot(result.Subscription),
	})
	common.ApiSuccess(c, gin.H{
		"message":      message,
		"subscription": result.Subscription,
	})
}

func AdminResetUserSubscriptionsByPlan(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	var req AdminResetSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if req.PlanId <= 0 {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	advanceResetTime := resolveAdvanceResetTime(req.AdvanceResetTime)
	result, err := model.AdminResetUserSubscriptionsByPlan(userId, req.PlanId, advanceResetTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordSubscriptionResetUserLogs(result, auditOperatorInfo(c))
	recordManageAuditFor(c, userId, "subscription.user_plan_reset", map[string]interface{}{
		"target_user_id":     userId,
		"plan_id":            result.PlanId,
		"plan_title":         result.PlanTitle,
		"reset_count":        result.ResetCount,
		"user_count":         result.UserCount,
		"advance_reset_time": result.AdvanceResetTime,
	})
	common.ApiSuccess(c, result)
}

func AdminResetPlanSubscriptions(c *gin.Context) {
	planId, _ := strconv.Atoi(c.Param("id"))
	if planId <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req AdminResetSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	advanceResetTime := resolveAdvanceResetTime(req.AdvanceResetTime)
	result, err := model.AdminResetPlanSubscriptions(planId, advanceResetTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordSubscriptionResetUserLogs(result, auditOperatorInfo(c))
	common.SysLog(fmt.Sprintf("admin reset subscription plan %d quota: reset_count=%d user_count=%d advance_reset_time=%t",
		result.PlanId, result.ResetCount, result.UserCount, result.AdvanceResetTime))
	recordManageAudit(c, "subscription.plan_reset", map[string]interface{}{
		"plan_id":            result.PlanId,
		"plan_title":         result.PlanTitle,
		"reset_count":        result.ResetCount,
		"user_count":         result.UserCount,
		"advance_reset_time": result.AdvanceResetTime,
	})
	common.ApiSuccess(c, result)
}

// AdminInvalidateUserSubscription cancels a user subscription immediately.
func AdminInvalidateUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	msg, err := model.AdminInvalidateUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
func AdminDeleteUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorMsg(c, "无效的订阅ID")
		return
	}
	msg, err := model.AdminDeleteUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if msg != "" {
		common.ApiSuccess(c, gin.H{"message": msg})
		return
	}
	common.ApiSuccess(c, nil)
}
