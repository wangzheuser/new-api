package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

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

// UserGroupGrantRevision identifies the ordered snapshot shown to an administrator.
func UserGroupGrantRevision(grants []UserGroupGrant) string {
	encoded, _ := common.Marshal(grants)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

// ReplaceUserGroupGrants saves the submitted changes against the exact version loaded by the administrator.
func ReplaceUserGroupGrants(userId int, revision string, grants []UserGroupGrant) (before, after []UserGroupGrant, err error) {
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userId).Error; err != nil {
			return err
		}
		before = make([]UserGroupGrant, 0)
		if err := tx.Where("user_id = ?", userId).Order("group_name asc").Find(&before).Error; err != nil {
			return err
		}
		if revision != UserGroupGrantRevision(before) {
			return common.NewMessage("These benefit groups have changed. Reload before saving.")
		}
		current := make(map[string]UserGroupGrant, len(before))
		for _, grant := range before {
			current[grant.GroupName] = grant
		}
		now := getDBTimestampTx(tx)
		for _, grant := range grants {
			existing, found := current[grant.GroupName]
			delete(current, grant.GroupName)
			if found && existing.ExpiresAt == grant.ExpiresAt {
				continue
			}
			if (grant.GroupName == user.Group || grant.GroupName == user.EffectiveBaseGroup()) || !ratio_setting.ContainsGroupRatio(grant.GroupName) ||
				(grant.ExpiresAt != 0 && grant.ExpiresAt <= now) {
				return common.NewMessage("The benefit group or its expiration is invalid")
			}
			if found {
				if err := tx.Model(&existing).Update("expires_at", grant.ExpiresAt).Error; err != nil {
					return err
				}
			} else {
				grant.UserId = userId
				if err := tx.Create(&grant).Error; err != nil {
					return err
				}
			}
		}
		for _, grant := range current {
			if err := tx.Delete(&grant).Error; err != nil {
				return err
			}
		}
		after = make([]UserGroupGrant, 0)
		return tx.Where("user_id = ?", userId).Order("group_name asc").Find(&after).Error
	})
	return before, after, err
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
