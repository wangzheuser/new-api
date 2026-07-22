package service

import (
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func GetUserUsableGroups(userGroup string) map[string]string {
	groupsCopy := setting.GetUserUsableGroupsCopy()
	if userGroup != "" {
		specialSettings, b := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
		if b {
			// 处理特殊可用分组
			for specialGroup, desc := range specialSettings {
				if strings.HasPrefix(specialGroup, "-:") {
					// 移除分组
					groupToRemove := strings.TrimPrefix(specialGroup, "-:")
					delete(groupsCopy, groupToRemove)
				} else if strings.HasPrefix(specialGroup, "+:") {
					// 添加分组
					groupToAdd := strings.TrimPrefix(specialGroup, "+:")
					groupsCopy[groupToAdd] = desc
				} else {
					// 直接添加分组
					groupsCopy[specialGroup] = desc
				}
			}
		}
		// 如果userGroup不在UserUsableGroups中，返回UserUsableGroups + userGroup
		if _, ok := groupsCopy[userGroup]; !ok {
			groupsCopy[userGroup] = "用户分组"
		}
	}
	return groupsCopy
}

func GroupInUserUsableGroups(userGroup, groupName string) bool {
	_, ok := GetUserUsableGroups(userGroup)[groupName]
	return ok
}

// GetUserEffectiveGroups returns the configured groups plus groups granted by active subscriptions.
func GetUserEffectiveGroups(userId int, userGroup string) (map[string]string, error) {
	groups := GetUserUsableGroups(userGroup)
	if userId <= 0 {
		return groups, nil
	}
	entitlementGroups, err := model.GetActiveUserEntitlementGroups(userId)
	if err != nil {
		return nil, err
	}
	for _, group := range entitlementGroups {
		group = strings.TrimSpace(group)
		if group != "" {
			if _, exists := groups[group]; !exists {
				groups[group] = "订阅权益分组"
			}
		}
	}
	return groups, nil
}

// GroupInUserEffectiveGroups reports whether a user can use the specified group now.
func GroupInUserEffectiveGroups(userId int, userGroup, groupName string) (bool, error) {
	if _, ok := GetUserUsableGroups(userGroup)[groupName]; ok {
		return true, nil
	}
	return model.HasActiveUserSubscriptionForGroup(userId, groupName)
}

// GetUserAutoGroup 根据用户分组获取自动分组设置
func GetUserAutoGroup(userGroup string) []string {
	groups := GetUserUsableGroups(userGroup)
	autoGroups := make([]string, 0)
	for _, group := range setting.GetAutoGroups() {
		if _, ok := groups[group]; ok {
			autoGroups = append(autoGroups, group)
		}
	}
	return autoGroups
}

// GetUserEffectiveAutoGroups returns auto groups available through configuration or subscriptions.
func GetUserEffectiveAutoGroups(userId int, userGroup string) ([]string, error) {
	groups, err := GetUserEffectiveGroups(userId, userGroup)
	if err != nil {
		return nil, err
	}
	autoGroups := make([]string, 0)
	for _, group := range setting.GetAutoGroups() {
		if _, ok := groups[group]; ok {
			autoGroups = append(autoGroups, group)
		}
	}
	return autoGroups, nil
}

// GetUserGroupRatio 获取用户使用某个分组的倍率
// userGroup 用户分组
// group 需要获取倍率的分组
func GetUserGroupRatio(userGroup, group string) float64 {
	ratio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group)
	if ok {
		return ratio
	}
	return ratio_setting.GetGroupRatio(group)
}
