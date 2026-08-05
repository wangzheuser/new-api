package controller

import (
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

type adminUserGroupGrantRequest struct {
	Grants []adminUserGroupGrantInput `json:"grants"`
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
	BaseGroup          string                       `json:"base_group"`
	SystemGroups       []string                     `json:"system_groups"`
	ManualGrants       []adminUserGroupGrantView    `json:"manual_grants"`
	SubscriptionGrants []subscriptionGroupGrantView `json:"subscription_grants"`
	EffectiveGroups    []string                     `json:"effective_groups"`
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
	for _, grant := range manualGrants {
		manualViews = append(manualViews, adminUserGroupGrantView{
			UserGroupGrant: grant,
			Active:         grant.ExpiresAt == 0 || grant.ExpiresAt > now,
		})
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
		if legacyGroup := strings.TrimSpace(subscription.EntitlementGroup); legacyGroup != "" {
			groups = model.MergeGroupNames(groups, model.GroupNames{legacyGroup})
		}
		if len(groups) == 0 {
			continue
		}
		subscriptionViews = append(subscriptionViews, subscriptionGroupGrantView{
			SubscriptionId: subscription.Id,
			PlanId:         subscription.PlanId,
			Groups:         groups,
			StartTime:      subscription.StartTime,
			EndTime:        subscription.EndTime,
			Status:         subscription.Status,
		})
	}

	configuredGroups := service.GetUserUsableGroups(user.Group)
	delete(configuredGroups, user.Group)
	systemGroups := make([]string, 0, len(configuredGroups))
	for group := range configuredGroups {
		systemGroups = append(systemGroups, group)
	}
	sort.Strings(systemGroups)

	effectiveGroupMap, err := service.GetUserEffectiveGroups(user.Id, user.Group)
	if err != nil {
		return nil, err
	}
	effectiveGroups := make([]string, 0, len(effectiveGroupMap))
	for group := range effectiveGroupMap {
		effectiveGroups = append(effectiveGroups, group)
	}
	sort.Strings(effectiveGroups)

	return &adminUserGroupGrantResponse{
		BaseGroup:          user.Group,
		SystemGroups:       systemGroups,
		ManualGrants:       manualViews,
		SubscriptionGrants: subscriptionViews,
		EffectiveGroups:    effectiveGroups,
	}, nil
}

// AdminGetUserGroupGrants returns manual, subscription, and effective group grants.
func AdminGetUserGroupGrants(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	user, ok := getManagedUser(c, userId)
	if !ok {
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
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	now := common.GetTimestamp()
	knownGroups := ratio_setting.GetGroupRatioCopy()
	seen := make(map[string]struct{}, len(request.Grants))
	grants := make([]model.UserGroupGrant, 0, len(request.Grants))
	for _, input := range request.Grants {
		group := strings.TrimSpace(input.Group)
		if group == "" {
			common.ApiErrorMsg(c, "权益分组不能为空")
			return
		}
		if _, exists := knownGroups[group]; !exists {
			common.ApiErrorMsg(c, "权益分组不存在: "+group)
			return
		}
		if group == user.Group {
			common.ApiErrorMsg(c, "基础分组已自动可用: "+group)
			return
		}
		if input.ExpiresAt < 0 || (input.ExpiresAt > 0 && input.ExpiresAt <= now) {
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

	before, err := model.GetUserGroupGrants(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.ReplaceUserGroupGrants(userId, grants); err != nil {
		common.ApiError(c, err)
		return
	}
	after, err := model.GetUserGroupGrants(userId)
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
