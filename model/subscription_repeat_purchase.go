package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	SubscriptionRepeatPurchaseIndependent          = "independent"
	SubscriptionRepeatPurchaseExtendTime           = "extend_time"
	SubscriptionRepeatPurchaseAddQuota             = "add_quota"
	SubscriptionRepeatPurchaseExtendTimeAddQuota   = "extend_time_add_quota"
	SubscriptionRepeatPurchaseMaxValidity          = "max_validity"
	SubscriptionRepeatPurchaseMaxValidityAddQuota  = "max_validity_add_quota"
	SubscriptionRepeatPurchaseExtendTimeResetQuota = "extend_time_reset_quota"
	SubscriptionRepeatPurchaseReplace              = "replace"

	SubscriptionApplyModePlanDefault = "plan_default"

	SubscriptionApplyActionCreated = "created"
	SubscriptionApplyActionMerged  = "merged"
)

// SubscriptionApplyOptions describes how one purchase or admin grant applies a plan.
type SubscriptionApplyOptions struct {
	Source               string
	ApplyMode            string
	EnforcePurchaseLimit bool
}

// SubscriptionApplyResult describes the entitlement changed by one application.
type SubscriptionApplyResult struct {
	Subscription *UserSubscription `json:"subscription"`
	Action       string            `json:"action"`
	AppliedMode  string            `json:"applied_mode"`
	PlanTitle    string            `json:"plan_title"`
	Before       *UserSubscription `json:"-"`
}

// NormalizeSubscriptionRepeatPurchaseMode returns a safe mode for persisted legacy values.
func NormalizeSubscriptionRepeatPurchaseMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if IsValidSubscriptionRepeatPurchaseMode(mode) {
		return mode
	}
	return SubscriptionRepeatPurchaseIndependent
}

// IsValidSubscriptionRepeatPurchaseMode reports whether mode is a supported plan mode.
func IsValidSubscriptionRepeatPurchaseMode(mode string) bool {
	switch strings.TrimSpace(mode) {
	case SubscriptionRepeatPurchaseIndependent,
		SubscriptionRepeatPurchaseExtendTime,
		SubscriptionRepeatPurchaseAddQuota,
		SubscriptionRepeatPurchaseExtendTimeAddQuota,
		SubscriptionRepeatPurchaseMaxValidity,
		SubscriptionRepeatPurchaseMaxValidityAddQuota,
		SubscriptionRepeatPurchaseExtendTimeResetQuota,
		SubscriptionRepeatPurchaseReplace:
		return true
	default:
		return false
	}
}

// ResolveSubscriptionApplyMode resolves an optional admin override against the plan default.
func ResolveSubscriptionApplyMode(applyMode string, planMode string) (string, error) {
	applyMode = strings.TrimSpace(applyMode)
	if applyMode == "" || applyMode == SubscriptionApplyModePlanDefault {
		return NormalizeSubscriptionRepeatPurchaseMode(planMode), nil
	}
	if !IsValidSubscriptionRepeatPurchaseMode(applyMode) {
		return "", fmt.Errorf("invalid subscription apply mode: %s", applyMode)
	}
	return applyMode, nil
}

// countUserSubscriptionAllocationsByPlanTx returns the historical allocation count for a user and plan.
func countUserSubscriptionAllocationsByPlanTx(tx *gorm.DB, userId int, planId int) (int64, error) {
	if tx == nil {
		return 0, errors.New("tx is nil")
	}
	var count int64
	err := tx.Model(&UserSubscription{}).
		Select("COALESCE(SUM(CASE WHEN allocation_count > 0 THEN allocation_count ELSE 1 END), 0)").
		Where("user_id = ? AND plan_id = ?", userId, planId).
		Scan(&count).Error
	return count, err
}

// mergeRepeatedUserSubscriptionTx merges incoming entitlement into the latest active subscription.
func mergeRepeatedUserSubscriptionTx(tx *gorm.DB, plan *SubscriptionPlan, incoming *UserSubscription, mode string) (*SubscriptionApplyResult, error) {
	if tx == nil || plan == nil || incoming == nil {
		return nil, errors.New("invalid subscription merge args")
	}
	var target UserSubscription
	query := lockForUpdate(tx).
		Where("user_id = ? AND plan_id = ? AND status = ? AND start_time <= ? AND end_time > ?",
			incoming.UserId, incoming.PlanId, "active", incoming.StartTime, incoming.StartTime).
		Order("end_time desc, id desc").
		Limit(1).
		Find(&target)
	if query.Error != nil {
		return nil, query.Error
	}
	if query.RowsAffected == 0 {
		return nil, nil
	}

	before := target
	allocationCount := target.AllocationCount
	if allocationCount < 1 {
		allocationCount = 1
	}
	if allocationCount == math.MaxInt64 {
		return nil, errors.New("subscription allocation count overflow")
	}

	if mode == SubscriptionRepeatPurchaseReplace {
		createdAt := target.CreatedAt
		previousGroup := target.PrevUserGroup
		target = *incoming
		target.Id = before.Id
		target.CreatedAt = createdAt
		target.AllocationCount = allocationCount + 1
		if previousGroup != "" {
			target.PrevUserGroup = previousGroup
		}
	} else {
		extendTime := mode == SubscriptionRepeatPurchaseExtendTime ||
			mode == SubscriptionRepeatPurchaseExtendTimeAddQuota ||
			mode == SubscriptionRepeatPurchaseExtendTimeResetQuota
		maxValidity := mode == SubscriptionRepeatPurchaseMaxValidity || mode == SubscriptionRepeatPurchaseMaxValidityAddQuota
		addQuota := mode == SubscriptionRepeatPurchaseAddQuota || mode == SubscriptionRepeatPurchaseExtendTimeAddQuota || mode == SubscriptionRepeatPurchaseMaxValidityAddQuota

		if extendTime {
			endTime, err := calcPlanEndTime(time.Unix(target.EndTime, 0), plan)
			if err != nil {
				return nil, err
			}
			target.EndTime = endTime
		} else if maxValidity && incoming.EndTime > target.EndTime {
			target.EndTime = incoming.EndTime
		}

		// Reopen a reset schedule only when this allocation actually extends validity.
		if target.EndTime > before.EndTime && target.NextResetTime == 0 && NormalizeResetPeriod(plan.QuotaResetPeriod) != SubscriptionResetNever {
			baseUnix := target.LastResetTime
			if baseUnix <= 0 {
				baseUnix = target.StartTime
			}
			base := time.Unix(baseUnix, 0)
			nextReset := calcNextResetTime(base, plan, target.EndTime)
			for nextReset > 0 && nextReset <= incoming.StartTime {
				base = time.Unix(nextReset, 0)
				nextReset = calcNextResetTime(base, plan, target.EndTime)
			}
			if nextReset > 0 {
				target.LastResetTime = base.Unix()
				target.NextResetTime = nextReset
			}
		}

		if addQuota {
			if target.AmountTotal == 0 || incoming.AmountTotal == 0 {
				target.AmountTotal = 0
			} else {
				if incoming.AmountTotal > math.MaxInt64-target.AmountTotal {
					return nil, errors.New("subscription quota overflow")
				}
				target.AmountTotal += incoming.AmountTotal
			}
		}
		if mode == SubscriptionRepeatPurchaseExtendTimeResetQuota {
			target.AmountTotal = incoming.AmountTotal
			target.AmountUsed = 0
		}

		target.AllocationCount = allocationCount + 1
		if incoming.UpgradeGroup != "" {
			target.UpgradeGroup = incoming.UpgradeGroup
			if target.PrevUserGroup == "" {
				target.PrevUserGroup = incoming.PrevUserGroup
			}
		}
		if incoming.DowngradeGroup != "" {
			target.DowngradeGroup = incoming.DowngradeGroup
		}
		target.AllowWalletOverflow = incoming.AllowWalletOverflow
	}

	if err := tx.Save(&target).Error; err != nil {
		return nil, err
	}
	return &SubscriptionApplyResult{
		Subscription: &target,
		Action:       SubscriptionApplyActionMerged,
		AppliedMode:  mode,
		PlanTitle:    plan.Title,
		Before:       &before,
	}, nil
}
