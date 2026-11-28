package controller

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type adminUserGroupGrantRequest struct {
	Grants   []adminUserGroupGrantInput `json:"grants"`
	Revision string                     `json:"revision"`
}

type adminUserGroupGrantInput struct {
	Group     string `json:"group"`
	ExpiresAt int64  `json:"expires_at"`
}

type adminUserGroupGrantView struct {
	model.UserGroupGrant
	Active bool `json:"active"`
}

type subscriptionGroupGrantView struct {
	SubscriptionId int              `json:"subscription_id"`
	PlanId         int              `json:"plan_id"`
	Groups         model.GroupNames `json:"groups"`
	StartTime      int64            `json:"start_time"`
	EndTime        int64            `json:"end_time"`
	Status         string           `json:"status"`
}

type adminUserGroupGrantResponse struct {
	Revision           string                       `json:"revision"`
	CurrentGroup       string                       `json:"current_group"`
	BaseGroup          string                       `json:"base_group"`
	SystemGroups       []string                     `json:"system_groups"`
	ManualGrants       []adminUserGroupGrantView    `json:"manual_grants"`
	SubscriptionGrants []subscriptionGroupGrantView `json:"subscription_grants"`
	EffectiveGroups    []string                     `json:"effective_groups"`
	BenefitGroups      model.GroupNames             `json:"benefit_groups"`
}

func availableBenefitGroups(grantedGroups, effectiveGroups []string) model.GroupNames {
	benefits := model.NormalizeGroupNames(grantedGroups)
	return slices.DeleteFunc(benefits, func(group string) bool {
		return group == "auto" || !slices.Contains(effectiveGroups, group)
	})
}

// getManagedUser validates the target user against the operator's role.
func getManagedUser(c *gin.Context, userId int) (*model.User, bool) {
	if userId <= 0 {
		common.ApiErrorMsg(c, "无效的用户ID")
		return nil, false
	}
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if !canManageTargetRole(c.GetInt("role"), user.Role) {
		common.ApiErrorMsg(c, "无权管理该用户")
		return nil, false
	}
	return user, true
}

// buildAdminUserGroupGrantResponse builds the current grant and effective-group view.
func buildAdminUserGroupGrantResponse(user *model.User) (*adminUserGroupGrantResponse, error) {
	now := common.GetTimestamp()
	manualGrants, err := model.GetUserGroupGrants(user.Id)
	if err != nil {
		return nil, err
	}
	manualViews := make([]adminUserGroupGrantView, 0, len(manualGrants))
	grantedGroups := make([]string, 0, len(manualGrants))
	for _, grant := range manualGrants {
		active := grant.ExpiresAt == 0 || grant.ExpiresAt > now
		manualViews = append(manualViews, adminUserGroupGrantView{
			UserGroupGrant: grant,
			Active:         active,
		})
		if active {
			grantedGroups = append(grantedGroups, grant.GroupName)
		}
	}

	subscriptions, err := model.GetAllActiveUserSubscriptions(user.Id)
	if err != nil {
		return nil, err
	}
	subscriptionViews := make([]subscriptionGroupGrantView, 0, len(subscriptions))
	for _, summary := range subscriptions {
		if summary.Subscription == nil {
			continue
		}
		subscription := summary.Subscription
		groups := model.NormalizeGroupNames(subscription.GrantGroups)
		if len(groups) == 0 {
			continue
		}
		grantedGroups = append(grantedGroups, groups...)
		subscriptionViews = append(subscriptionViews, subscriptionGroupGrantView{
			SubscriptionId: subscription.Id,
			PlanId:         subscription.PlanId,
			Groups:         groups,
			StartTime:      subscription.StartTime,
			EndTime:        subscription.EndTime,
			Status:         subscription.Status,
		})
	}

	configuredGroups := service.GetUserUsableGroups(user.EffectiveBaseGroup())
	delete(configuredGroups, user.EffectiveBaseGroup())
	systemGroups := make([]string, 0, len(configuredGroups))
	for group := range configuredGroups {
		systemGroups = append(systemGroups, group)
	}
	sort.Strings(systemGroups)

	effectiveGroupMap, err := service.GetUserEffectiveGroups(user.Id, user.EffectiveBaseGroup())
	if err != nil {
		return nil, err
	}
	effectiveGroups := make([]string, 0, len(effectiveGroupMap))
	for group := range effectiveGroupMap {
		effectiveGroups = append(effectiveGroups, group)
	}
	sort.Strings(effectiveGroups)

	return &adminUserGroupGrantResponse{
		Revision:           model.UserGroupGrantRevision(manualGrants),
		BaseGroup:          user.Group,
		CurrentGroup:       user.EffectiveBaseGroup(),
		SystemGroups:       systemGroups,
		ManualGrants:       manualViews,
		SubscriptionGrants: subscriptionViews,
		EffectiveGroups:    effectiveGroups,
		BenefitGroups:      availableBenefitGroups(grantedGroups, effectiveGroups),
	}, nil
}

// AdminGetUserGroupGrants returns manual, subscription, and effective group grants.
func AdminGetUserGroupGrants(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	user, ok := getManagedUser(c, userId)
	if !ok {
		return
	}
	if err := model.ReconcileDueUserSubscriptions(userId); err != nil {
		common.ApiError(c, err)
		return
	}
	user, err := model.GetUserById(userId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	response, err := buildAdminUserGroupGrantResponse(user)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}

// AdminReplaceUserGroupGrants replaces administrator-managed group grants for a user.
func AdminReplaceUserGroupGrants(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	user, ok := getManagedUser(c, userId)
	if !ok {
		return
	}
	var request adminUserGroupGrantRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.Grants == nil || request.Revision == "" {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	seen := make(map[string]struct{}, len(request.Grants))
	grants := make([]model.UserGroupGrant, 0, len(request.Grants))
	for _, input := range request.Grants {
		group := strings.TrimSpace(input.Group)
		if group == "" {
			common.ApiErrorMsg(c, "权益分组不能为空")
			return
		}
		if input.ExpiresAt < 0 {
			common.ApiErrorMsg(c, "权益到期时间必须为永久或未来时间")
			return
		}
		if _, duplicate := seen[group]; duplicate {
			common.ApiErrorMsg(c, "权益分组重复: "+group)
			return
		}
		seen[group] = struct{}{}
		grants = append(grants, model.UserGroupGrant{GroupName: group, ExpiresAt: input.ExpiresAt})
	}
	sort.Slice(grants, func(left, right int) bool {
		return grants[left].GroupName < grants[right].GroupName
	})

	before, after, err := model.ReplaceUserGroupGrants(userId, request.Revision, grants)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, userId, "user.group_grants_update", map[string]interface{}{
		"before": before,
		"after":  after,
	})
	response, err := buildAdminUserGroupGrantResponse(user)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, response)
}
