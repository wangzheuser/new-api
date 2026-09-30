package model

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ApplyBillingDelta 在同一事务中调整资金来源和令牌，避免只完成一侧扣费。
// tokenId 为 0 时只调整资金，用于 Playground；差额不进入异步批量队列。
func ApplyBillingDelta(userId, tokenId int, tokenKey string, subscriptionId, delta int) error {
	if delta == 0 {
		return nil
	}
	if userId <= 0 || tokenId < 0 || subscriptionId < 0 || int64(delta) < -math.MaxInt32 || int64(delta) > math.MaxInt32 {
		return fmt.Errorf("invalid billing adjustment")
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if subscriptionId > 0 {
			if err := postConsumeUserSubscriptionDelta(tx, subscriptionId, int64(delta)); err != nil {
				return err
			}
		} else {
			userQuery := tx.Model(&User{}).Where("id = ?", userId)
			if delta > 0 {
				userQuery = userQuery.Where("quota >= ?", delta)
			}
			result := userQuery.
				UpdateColumn("quota", gorm.Expr("quota - ?", delta))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("billing user %d quota is insufficient or user is missing", userId)
			}
		}
		if tokenId > 0 {
			tokenQuery := tx.Model(&Token{}).Where("id = ? AND user_id = ?", tokenId, userId)
			if delta > 0 {
				tokenQuery = tokenQuery.Where("(unlimited_quota = ? OR remain_quota >= ?) AND used_quota <= ?", true, delta, math.MaxInt32-delta)
			}
			result := tokenQuery.Updates(map[string]interface{}{
				"remain_quota":  gorm.Expr("remain_quota - ?", delta),
				"used_quota":    gorm.Expr("used_quota + ?", delta),
				"accessed_time": common.GetTimestamp(),
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("billing token %d quota is insufficient or token is missing", tokenId)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 数据库提交后才更新缓存；缓存失败不会把已提交的扣费报告为可重试失败。
	if common.RedisEnabled {
		if subscriptionId == 0 {
			if err := cacheDecrUserQuota(userId, int64(delta)); err != nil {
				common.SysError("billing user cache update failed: " + err.Error())
				_ = invalidateUserCache(userId)
			}
		}
		if tokenId > 0 {
			if err := cacheDecrTokenQuota(tokenKey, int64(delta)); err != nil {
				common.SysError("billing token cache update failed: " + err.Error())
				_ = cacheDeleteToken(tokenKey)
			}
		}
	}
	return nil
}

// RefundSubscriptionBilling 原子退还订阅预扣和令牌额度。
// 记录只有在两侧都写入成功后才标记 refunded，事务失败可安全重试。
func RefundSubscriptionBilling(requestID string, userID, tokenID int, tokenKey string, extraReserved int64) error {
	if strings.TrimSpace(requestID) == "" || userID <= 0 || tokenID < 0 || extraReserved < 0 {
		return fmt.Errorf("invalid subscription refund")
	}
	var cacheRefund int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var record SubscriptionPreConsumeRecord
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&record).Error; err != nil {
			return err
		}
		if record.Status == "refunded" {
			return nil
		}
		if err := postConsumeUserSubscriptionDelta(tx, record.UserSubscriptionId, -(record.PreConsumed + extraReserved)); err != nil {
			return err
		}
		record.Status = "refunded"
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		cacheRefund = record.PreConsumed + extraReserved
		if tokenID > 0 {
			result := tx.Model(&Token{}).Where("id = ? AND user_id = ?", tokenID, userID).Updates(map[string]interface{}{
				"remain_quota":  gorm.Expr("remain_quota + ?", cacheRefund),
				"used_quota":    gorm.Expr("used_quota - ?", cacheRefund),
				"accessed_time": common.GetTimestamp(),
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("billing token %d not found", tokenID)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if common.RedisEnabled && tokenID > 0 {
		if err := cacheIncrTokenQuota(tokenKey, cacheRefund); err != nil {
			common.SysError("billing token refund cache update failed: " + err.Error())
			_ = cacheDeleteToken(tokenKey)
		}
	}
	return nil
}
