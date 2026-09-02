package controller

import (
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type adminUserInvitationOverview struct {
	Code           string `json:"code"`
	InviterId      int    `json:"inviter_id"`
	InvitedCount   int    `json:"invited_count"`
	AvailableQuota int    `json:"available_quota"`
	HistoryQuota   int    `json:"history_quota"`
}

type adminUserSubscriptionOverview struct {
	Id                  int              `json:"id"`
	PlanId              int              `json:"plan_id"`
	PlanTitle           string           `json:"plan_title"`
	Status              string           `json:"status"`
	Source              string           `json:"source"`
	StartTime           int64            `json:"start_time"`
	EndTime             int64            `json:"end_time"`
	AmountTotal         int64            `json:"amount_total"`
	AmountUsed          int64            `json:"amount_used"`
	AllocationCount     int64            `json:"allocation_count"`
	NextResetTime       int64            `json:"next_reset_time"`
	BenefitGroups       model.GroupNames `json:"benefit_groups"`
	AllowWalletOverflow bool             `json:"allow_wallet_overflow"`
}

type adminUserOverview struct {
	Id                     int                             `json:"id"`
	Username               string                          `json:"username"`
	DisplayName            string                          `json:"display_name"`
	Email                  string                          `json:"email"`
	Role                   int                             `json:"role"`
	Status                 int                             `json:"status"`
	BaseGroup              string                          `json:"base_group"`
	EffectiveGroups        []string                        `json:"effective_groups"`
	Quota                  int                             `json:"quota"`
	UsedQuota              int                             `json:"used_quota"`
	RequestCount           int                             `json:"request_count"`
	BillingPreference      string                          `json:"billing_preference"`
	CreatedAt              int64                           `json:"created_at"`
	LastLoginAt            int64                           `json:"last_login_at"`
	Remark                 string                          `json:"remark"`
	Invitation             adminUserInvitationOverview     `json:"invitation"`
	ActiveSubscriptions    []adminUserSubscriptionOverview `json:"active_subscriptions"`
	ScheduledSubscriptions []adminUserSubscriptionOverview `json:"scheduled_subscriptions"`
}

// buildAdminUserOverview creates the explicit, read-only response used by the log user dialog.
func buildAdminUserOverview(user *model.User, subscriptions []model.SubscriptionSummary, planTitles map[int]string, effectiveGroups []string, now int64) adminUserOverview {
	activeSubscriptions := make([]adminUserSubscriptionOverview, 0)
	scheduledSubscriptions := make([]adminUserSubscriptionOverview, 0)
	if effectiveGroups == nil {
		effectiveGroups = make([]string, 0)
	}
	for _, summary := range subscriptions {
		if summary.Subscription == nil {
			continue
		}
		subscription := summary.Subscription
		view := adminUserSubscriptionOverview{
			Id:                  subscription.Id,
			PlanId:              subscription.PlanId,
			PlanTitle:           planTitles[subscription.PlanId],
			Status:              subscription.Status,
			Source:              subscription.Source,
			StartTime:           subscription.StartTime,
			EndTime:             subscription.EndTime,
			AmountTotal:         subscription.AmountTotal,
			AmountUsed:          subscription.AmountUsed,
			AllocationCount:     subscription.AllocationCount,
			NextResetTime:       subscription.NextResetTime,
			BenefitGroups:       model.MergeGroupNames(model.GroupNames{subscription.EntitlementGroup}, subscription.GrantGroups),
			AllowWalletOverflow: subscription.AllowWalletOverflow,
		}
		if subscription.Status == "active" && subscription.StartTime <= now && subscription.EndTime > now {
			activeSubscriptions = append(activeSubscriptions, view)
			continue
		}
		if subscription.Status == "scheduled" && subscription.StartTime > now && subscription.EndTime > now {
			scheduledSubscriptions = append(scheduledSubscriptions, view)
		}
	}
	sort.Slice(activeSubscriptions, func(i, j int) bool {
		if activeSubscriptions[i].EndTime == activeSubscriptions[j].EndTime {
			return activeSubscriptions[i].Id < activeSubscriptions[j].Id
		}
		return activeSubscriptions[i].EndTime < activeSubscriptions[j].EndTime
	})
	sort.Slice(scheduledSubscriptions, func(i, j int) bool {
		if scheduledSubscriptions[i].StartTime == scheduledSubscriptions[j].StartTime {
			return scheduledSubscriptions[i].Id < scheduledSubscriptions[j].Id
		}
		return scheduledSubscriptions[i].StartTime < scheduledSubscriptions[j].StartTime
	})
	sort.Strings(effectiveGroups)

	return adminUserOverview{
		Id:                user.Id,
		Username:          user.Username,
		DisplayName:       user.DisplayName,
		Email:             user.Email,
		Role:              user.Role,
		Status:            user.Status,
		BaseGroup:         user.Group,
		EffectiveGroups:   effectiveGroups,
		Quota:             user.Quota,
		UsedQuota:         user.UsedQuota,
		RequestCount:      user.RequestCount,
		BillingPreference: common.NormalizeBillingPreference(user.GetSetting().BillingPreference),
		CreatedAt:         user.CreatedAt,
		LastLoginAt:       user.LastLoginAt,
		Remark:            user.Remark,
		Invitation: adminUserInvitationOverview{
			Code:           user.AffCode,
			InviterId:      user.InviterId,
			InvitedCount:   user.AffCount,
			AvailableQuota: user.AffQuota,
			HistoryQuota:   user.AffHistoryQuota,
		},
		ActiveSubscriptions:    activeSubscriptions,
		ScheduledSubscriptions: scheduledSubscriptions,
	}
}

// GetAdminUserOverview returns the account and current subscription snapshot used by administrators in usage logs.
func GetAdminUserOverview(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return
	}
	user, ok := getManagedUser(c, userId)
	if !ok {
		return
	}
	if err := model.ReconcileDueUserSubscriptions(userId); err != nil {
		common.ApiError(c, err)
		return
	}
	// Reconciliation can activate a subscription and update the user's base group.
	user, err = model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	now := common.GetTimestamp()
	subscriptions, err := model.GetCurrentAndScheduledUserSubscriptions(userId, now)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	planIds := make([]int, 0, len(subscriptions))
	seenPlanIds := make(map[int]struct{})
	for _, summary := range subscriptions {
		if summary.Subscription == nil || summary.Subscription.PlanId <= 0 {
			continue
		}
		if _, exists := seenPlanIds[summary.Subscription.PlanId]; exists {
			continue
		}
		seenPlanIds[summary.Subscription.PlanId] = struct{}{}
		planIds = append(planIds, summary.Subscription.PlanId)
	}
	planTitles, err := model.GetSubscriptionPlanTitles(planIds)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	effectiveGroupMap, err := service.GetUserEffectiveGroups(userId, user.Group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	effectiveGroups := make([]string, 0, len(effectiveGroupMap))
	for group := range effectiveGroupMap {
		if group = strings.TrimSpace(group); group != "" {
			effectiveGroups = append(effectiveGroups, group)
		}
	}
	common.ApiSuccess(c, buildAdminUserOverview(user, subscriptions, planTitles, effectiveGroups, now))
}
