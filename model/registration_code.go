package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrRegistrationCodeRequired  = errors.New("registration code is required")
	ErrRegistrationCodeInvalid   = errors.New("registration code is invalid")
	ErrRegistrationCodeDisabled  = errors.New("registration code is disabled")
	ErrRegistrationCodeNotOpen   = errors.New("registration code is not open")
	ErrRegistrationCodeExpired   = errors.New("registration code is expired")
	ErrRegistrationCodeExhausted = errors.New("registration code is exhausted")
)

// RegistrationCode controls whether a new account may be created.
type RegistrationCode struct {
	Id           int            `json:"id"`
	UserId       int            `json:"user_id" gorm:"index"`
	Code         string         `json:"code" gorm:"type:varchar(64);uniqueIndex"`
	Status       int            `json:"status" gorm:"index"`
	Name         string         `json:"name" gorm:"type:varchar(64);index"`
	MaxUses      int            `json:"max_uses"`
	UsedCount    int            `json:"used_count"`
	OpenTime     int64          `json:"open_time" gorm:"bigint"`
	EndTime      int64          `json:"end_time" gorm:"bigint"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint;index"`
	LastUsedTime int64          `json:"last_used_time" gorm:"bigint"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
	Count        int            `json:"count" gorm:"-:all"`
}

// RegistrationCodeUsage records which account consumed a registration code.
type RegistrationCodeUsage struct {
	Id                 int    `json:"id"`
	RegistrationCodeId int    `json:"registration_code_id" gorm:"index"`
	UserId             int    `json:"user_id" gorm:"index"`
	Username           string `json:"username" gorm:"type:varchar(64);index"`
	Source             string `json:"source" gorm:"type:varchar(64);index"`
	UsedTime           int64  `json:"used_time" gorm:"bigint;index"`
}

// RegistrationCodeQuery contains root-side list filters.
type RegistrationCodeQuery struct {
	Keyword  string
	Status   int
	Usage    string
	Validity string
	Now      int64
}

// ConsumeRegistrationCodeTx validates and consumes a code in the caller's transaction.
func ConsumeRegistrationCodeTx(tx *gorm.DB, code string, userId int, username string, source string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		if common.RegistrationCodeRequired {
			return ErrRegistrationCodeRequired
		}
		return nil
	}
	if len([]rune(code)) > 64 || userId <= 0 {
		return ErrRegistrationCodeInvalid
	}

	now := common.GetTimestamp()
	var registrationCode RegistrationCode
	if err := tx.Where("code = ?", code).First(&registrationCode).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRegistrationCodeInvalid
		}
		return err
	}
	if registrationCode.Status != common.RegistrationCodeStatusEnabled {
		return ErrRegistrationCodeDisabled
	}
	if registrationCode.OpenTime > now {
		return ErrRegistrationCodeNotOpen
	}
	if registrationCode.EndTime != 0 && registrationCode.EndTime < now {
		return ErrRegistrationCodeExpired
	}
	if registrationCode.MaxUses <= 0 || registrationCode.UsedCount >= registrationCode.MaxUses {
		return ErrRegistrationCodeExhausted
	}

	result := tx.Model(&RegistrationCode{}).
		Where("id = ? AND status = ? AND open_time <= ? AND (end_time = 0 OR end_time >= ?) AND used_count < max_uses",
			registrationCode.Id, common.RegistrationCodeStatusEnabled, now, now).
		Updates(map[string]interface{}{
			"used_count":     gorm.Expr("used_count + ?", 1),
			"last_used_time": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRegistrationCodeExhausted
	}

	return tx.Create(&RegistrationCodeUsage{
		RegistrationCodeId: registrationCode.Id,
		UserId:             userId,
		Username:           username,
		Source:             source,
		UsedTime:           now,
	}).Error
}

// GetRegistrationCodes returns a filtered page of registration codes.
func GetRegistrationCodes(filter RegistrationCodeQuery, startIdx int, num int) ([]*RegistrationCode, int64, error) {
	query := DB.Model(&RegistrationCode{})
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		keyword = "%" + strings.ToLower(keyword) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(code) LIKE ?", keyword, keyword)
	}
	if filter.Status != 0 {
		query = query.Where("status = ?", filter.Status)
	}
	switch filter.Usage {
	case "unused":
		query = query.Where("used_count = 0")
	case "partial":
		query = query.Where("used_count > 0 AND used_count < max_uses")
	case "exhausted":
		query = query.Where("used_count >= max_uses")
	}
	now := filter.Now
	if now == 0 {
		now = common.GetTimestamp()
	}
	switch filter.Validity {
	case "pending":
		query = query.Where("open_time > ?", now)
	case "active":
		query = query.Where("open_time <= ? AND (end_time = 0 OR end_time >= ?)", now, now)
	case "expired":
		query = query.Where("end_time > 0 AND end_time < ?", now)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var codes []*RegistrationCode
	err := query.Order("id desc").Offset(startIdx).Limit(num).Find(&codes).Error
	return codes, total, err
}

// GetRegistrationCodeUsages returns the usage history for one code.
func GetRegistrationCodeUsages(codeId int, startIdx int, num int) ([]*RegistrationCodeUsage, int64, error) {
	query := DB.Model(&RegistrationCodeUsage{}).Where("registration_code_id = ?", codeId)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var usages []*RegistrationCodeUsage
	err := query.Order("id desc").Offset(startIdx).Limit(num).Find(&usages).Error
	return usages, total, err
}

// UpdateRegistrationCode updates mutable management fields.
func UpdateRegistrationCode(code *RegistrationCode) error {
	return DB.Model(&RegistrationCode{}).Where("id = ?", code.Id).Updates(map[string]interface{}{
		"name":      code.Name,
		"status":    code.Status,
		"max_uses":  code.MaxUses,
		"open_time": code.OpenTime,
		"end_time":  code.EndTime,
	}).Error
}

// DeleteRegistrationCode deletes a registration code and keeps its usage audit rows.
func DeleteRegistrationCode(id int) error {
	return DB.Delete(&RegistrationCode{}, id).Error
}

// BatchUpdateRegistrationCodeStatus changes the status of selected codes.
func BatchUpdateRegistrationCodeStatus(ids []int, status int) (int64, error) {
	result := DB.Model(&RegistrationCode{}).
		Where("id IN ? AND status <> ?", ids, status).
		Update("status", status)
	return result.RowsAffected, result.Error
}

// BatchDeleteRegistrationCodes soft-deletes selected codes and retains usage rows.
func BatchDeleteRegistrationCodes(ids []int) (int64, error) {
	result := DB.Where("id IN ?", ids).Delete(&RegistrationCode{})
	return result.RowsAffected, result.Error
}
