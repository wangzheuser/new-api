package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// UserGroupGrant stores one administrator-managed group grant for a user.
type UserGroupGrant struct {
	Id        int    `json:"id"`
	UserId    int    `json:"user_id" gorm:"not null;uniqueIndex:idx_user_group_grant,priority:1"`
	GroupName string `json:"group" gorm:"column:group_name;type:varchar(64);not null;uniqueIndex:idx_user_group_grant,priority:2"`
	ExpiresAt int64  `json:"expires_at" gorm:"type:bigint;not null;default:0"`
	CreatedAt int64  `json:"created_at" gorm:"type:bigint"`
	UpdatedAt int64  `json:"updated_at" gorm:"type:bigint"`
}

// BeforeCreate initializes grant timestamps.
func (grant *UserGroupGrant) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	grant.CreatedAt = now
	grant.UpdatedAt = now
	return nil
}

// BeforeUpdate refreshes the grant modification timestamp.
func (grant *UserGroupGrant) BeforeUpdate(tx *gorm.DB) error {
	grant.UpdatedAt = common.GetTimestamp()
	return nil
}

// GetUserGroupGrants returns all administrator-managed grants for a user.
func GetUserGroupGrants(userId int) ([]UserGroupGrant, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	grants := make([]UserGroupGrant, 0)
	err := DB.Where("user_id = ?", userId).Order("group_name asc").Find(&grants).Error
	return grants, err
}

// ReplaceUserGroupGrants atomically replaces a user's administrator-managed grants.
func ReplaceUserGroupGrants(userId int, grants []UserGroupGrant) error {
	if userId <= 0 {
		return errors.New("invalid userId")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userId).Delete(&UserGroupGrant{}).Error; err != nil {
			return err
		}
		if len(grants) == 0 {
			return nil
		}
		for index := range grants {
			grants[index].Id = 0
			grants[index].UserId = userId
			grants[index].GroupName = strings.TrimSpace(grants[index].GroupName)
		}
		return tx.Create(&grants).Error
	})
}

// GetActiveUserManualGrantGroups returns currently effective manual group grants.
func GetActiveUserManualGrantGroups(userId int, now int64) ([]string, error) {
	if userId <= 0 {
		return []string{}, nil
	}
	groups := make([]string, 0)
	err := DB.Model(&UserGroupGrant{}).
		Where("user_id = ? AND (expires_at = 0 OR expires_at > ?)", userId, now).
		Order("group_name asc").
		Pluck("group_name", &groups).Error
	return groups, err
}

// GetActiveUserGrantedGroups returns the union of manual and subscription group grants.
func GetActiveUserGrantedGroups(userId int) ([]string, error) {
	if userId <= 0 {
		return []string{}, nil
	}
	now := common.GetTimestamp()
	manualGroups, err := GetActiveUserManualGrantGroups(userId, now)
	if err != nil {
		return nil, err
	}
	subscriptionGroups, err := GetActiveUserEntitlementGroups(userId)
	if err != nil {
		return nil, err
	}
	return []string(MergeGroupNames(GroupNames(manualGroups), GroupNames(subscriptionGroups))), nil
}
