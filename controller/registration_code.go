package controller

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// respondRegistrationCodeError maps model validation errors to localized API messages.
func respondRegistrationCodeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrRegistrationCodeRequired):
		common.ApiErrorI18n(c, i18n.MsgRegistrationCodeRequired)
	case errors.Is(err, model.ErrRegistrationCodeInvalid):
		common.ApiErrorI18n(c, i18n.MsgRegistrationCodeInvalid)
	case errors.Is(err, model.ErrRegistrationCodeDisabled):
		common.ApiErrorI18n(c, i18n.MsgRegistrationCodeDisabled)
	case errors.Is(err, model.ErrRegistrationCodeNotOpen):
		common.ApiErrorI18n(c, i18n.MsgRegistrationCodeNotOpen)
	case errors.Is(err, model.ErrRegistrationCodeExpired):
		common.ApiErrorI18n(c, i18n.MsgRegistrationCodeExpired)
	case errors.Is(err, model.ErrRegistrationCodeExhausted):
		common.ApiErrorI18n(c, i18n.MsgRegistrationCodeExhausted)
	default:
		common.ApiError(c, err)
	}
}

// isRegistrationCodeError reports whether an error is safe to expose as a registration-code result.
func isRegistrationCodeError(err error) bool {
	return errors.Is(err, model.ErrRegistrationCodeRequired) ||
		errors.Is(err, model.ErrRegistrationCodeInvalid) ||
		errors.Is(err, model.ErrRegistrationCodeDisabled) ||
		errors.Is(err, model.ErrRegistrationCodeNotOpen) ||
		errors.Is(err, model.ErrRegistrationCodeExpired) ||
		errors.Is(err, model.ErrRegistrationCodeExhausted)
}

// GetRegistrationCodes returns a paginated list for root management.
func GetRegistrationCodes(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	statusText := c.Query("status")
	status, _ := strconv.Atoi(statusText)
	if statusText != "" && status != common.RegistrationCodeStatusEnabled && status != common.RegistrationCodeStatusDisabled {
		common.ApiError(c, errors.New("invalid registration code status"))
		return
	}
	usage := c.Query("usage")
	if usage != "" && usage != "unused" && usage != "partial" && usage != "exhausted" {
		common.ApiError(c, errors.New("invalid registration code usage filter"))
		return
	}
	validity := c.Query("validity")
	if validity != "" && validity != "pending" && validity != "active" && validity != "expired" {
		common.ApiError(c, errors.New("invalid registration code validity filter"))
		return
	}
	codes, total, err := model.GetRegistrationCodes(model.RegistrationCodeQuery{
		Keyword:  c.Query("keyword"),
		Status:   status,
		Usage:    usage,
		Validity: validity,
	}, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(codes)
	common.ApiSuccess(c, pageInfo)
}

// GetRegistrationCodeUsages returns usage history for one registration code.
func GetRegistrationCodeUsages(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid registration code id"))
		return
	}
	pageInfo := common.GetPageQuery(c)
	usages, total, err := model.GetRegistrationCodeUsages(id, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(usages)
	common.ApiSuccess(c, pageInfo)
}

// AddRegistrationCodes creates one or more registration codes.
func AddRegistrationCodes(c *gin.Context) {
	req := model.RegistrationCode{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Count < 1 || req.Count > 100 {
		common.ApiError(c, errors.New("count must be between 1 and 100"))
		return
	}
	if req.MaxUses < 1 {
		common.ApiError(c, errors.New("max_uses must be greater than 0"))
		return
	}
	if req.OpenTime < 0 || req.EndTime < 0 || (req.EndTime > 0 && req.EndTime < req.OpenTime) {
		common.ApiError(c, errors.New("invalid registration code time range"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 64 {
		common.ApiError(c, errors.New("name length must be between 1 and 64"))
		return
	}

	now := common.GetTimestamp()
	codes := make([]model.RegistrationCode, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		codes = append(codes, model.RegistrationCode{
			UserId:      c.GetInt("id"),
			Code:        common.GetUUID(),
			Status:      common.RegistrationCodeStatusEnabled,
			Name:        name,
			MaxUses:     req.MaxUses,
			OpenTime:    req.OpenTime,
			EndTime:     req.EndTime,
			CreatedTime: now,
		})
	}
	if err := model.DB.Create(&codes).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_code.create", map[string]interface{}{
		"count": req.Count, "name": name, "max_uses": req.MaxUses,
	})
	common.ApiSuccess(c, codes)
}

// UpdateRegistrationCode updates one registration code.
func UpdateRegistrationCode(c *gin.Context) {
	req := model.RegistrationCode{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Id <= 0 || req.MaxUses < 1 || req.UsedCount > req.MaxUses {
		common.ApiError(c, errors.New("invalid registration code settings"))
		return
	}
	if req.Status != common.RegistrationCodeStatusEnabled && req.Status != common.RegistrationCodeStatusDisabled {
		common.ApiError(c, errors.New("invalid registration code status"))
		return
	}
	if req.OpenTime < 0 || req.EndTime < 0 || (req.EndTime > 0 && req.EndTime < req.OpenTime) {
		common.ApiError(c, errors.New("invalid registration code time range"))
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 64 {
		common.ApiError(c, errors.New("name length must be between 1 and 64"))
		return
	}
	if err := model.UpdateRegistrationCode(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_code.update", map[string]interface{}{"id": req.Id})
	common.ApiSuccess(c, req)
}

// DeleteRegistrationCode deletes one registration code.
func DeleteRegistrationCode(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid registration code id"))
		return
	}
	if err := model.DeleteRegistrationCode(id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_code.delete", map[string]interface{}{"id": id})
	common.ApiSuccess(c, nil)
}

type registrationCodeBatchRequest struct {
	Ids    []int `json:"ids"`
	Status int   `json:"status"`
}

// normalizeRegistrationCodeIds validates and de-duplicates batch identifiers.
func normalizeRegistrationCodeIds(ids []int) ([]int, error) {
	if len(ids) == 0 {
		return nil, errors.New("ids must contain between 1 and 100 items")
	}
	seen := make(map[int]struct{}, len(ids))
	result := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, errors.New("ids must contain positive integers")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
		if len(result) > 100 {
			return nil, errors.New("ids must contain between 1 and 100 items")
		}
	}
	return result, nil
}

// BatchUpdateRegistrationCodeStatus enables or disables selected codes.
func BatchUpdateRegistrationCodeStatus(c *gin.Context) {
	req := registrationCodeBatchRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	ids, err := normalizeRegistrationCodeIds(req.Ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Status != common.RegistrationCodeStatusEnabled && req.Status != common.RegistrationCodeStatusDisabled {
		common.ApiError(c, errors.New("invalid registration code status"))
		return
	}
	changed, err := model.BatchUpdateRegistrationCodeStatus(ids, req.Status)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_code.status_update_batch", map[string]interface{}{
		"count": changed, "total": len(ids), "status": req.Status,
	})
	common.ApiSuccess(c, changed)
}

// BatchDeleteRegistrationCodes soft-deletes selected codes.
func BatchDeleteRegistrationCodes(c *gin.Context) {
	req := registrationCodeBatchRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	ids, err := normalizeRegistrationCodeIds(req.Ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	deleted, err := model.BatchDeleteRegistrationCodes(ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_code.delete_batch", map[string]interface{}{
		"count": deleted, "total": len(ids),
	})
	common.ApiSuccess(c, deleted)
}

// UpdateRegistrationCodeConfig changes whether new accounts require a code.
func UpdateRegistrationCodeConfig(c *gin.Context) {
	var req struct {
		Required bool `json:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOptionsBulk(map[string]string{
		"RegistrationCodeRequired": strconv.FormatBool(req.Required),
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_code.config", map[string]interface{}{"required": req.Required})
	common.ApiSuccess(c, gin.H{"required": req.Required})
}
