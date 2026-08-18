package controller

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type userModelRateLimitGroupResponse struct {
	Group        string `json:"group"`
	StatusCode   int    `json:"status_code"`
	ErrorMessage string `json:"error_message"`
}

type userModelRateLimitConfigRequest struct {
	DelaySeconds    int                               `json:"delay_seconds"`
	DefaultResponse dto.ModelRateLimitResponse        `json:"default_response"`
	GroupResponses  []userModelRateLimitGroupResponse `json:"group_responses"`
}

type userModelRateLimitUserView struct {
	Id          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Status      int    `json:"status"`
}

type userModelRateLimitEffectiveResponse struct {
	dto.ModelRateLimitResponse
	Source string `json:"source"`
}

type userModelRateLimitRuleView struct {
	Id                int                                 `json:"id"`
	User              userModelRateLimitUserView          `json:"user"`
	Group             string                              `json:"group"`
	TotalCount        int                                 `json:"total_count"`
	SuccessCount      int                                 `json:"success_count"`
	Response          *dto.ModelRateLimitResponse         `json:"response"`
	EffectiveResponse userModelRateLimitEffectiveResponse `json:"effective_response"`
	CreatedAt         int64                               `json:"created_at"`
	UpdatedAt         int64                               `json:"updated_at"`
}

// GetUserModelRateLimitConfig returns the editable response policy and read-only base limiter state.
func GetUserModelRateLimitConfig(c *gin.Context) {
	config := setting.GetUserModelRateLimitResponseConfig()
	common.ApiSuccess(c, gin.H{
		"base_limit": gin.H{
			"enabled":        setting.ModelRequestRateLimitEnabled,
			"period_minutes": setting.ModelRequestRateLimitDurationMinutes,
			"total_count":    setting.ModelRequestRateLimitCount,
			"success_count":  setting.ModelRequestRateLimitSuccessCount,
		},
		"delay_seconds":    config.DelaySeconds,
		"default_response": config.DefaultResponse,
		"group_responses":  sortedUserModelRateLimitGroupResponses(config.GroupResponses),
	})
}

// UpdateUserModelRateLimitConfig atomically replaces the shared response and delay configuration.
func UpdateUserModelRateLimitConfig(c *gin.Context) {
	var request userModelRateLimitConfigRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		userModelRateLimitAPIError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	current := setting.GetUserModelRateLimitResponseConfig()
	groupResponses := make(map[string]dto.ModelRateLimitResponse, len(request.GroupResponses))
	availableGroups := ratio_setting.GetGroupRatioCopy()
	for _, item := range request.GroupResponses {
		group := strings.TrimSpace(item.Group)
		if _, exists := groupResponses[group]; exists {
			userModelRateLimitAPIError(c, http.StatusBadRequest, "duplicate group: "+group)
			return
		}
		if group != "auto" {
			_, available := availableGroups[group]
			_, retained := current.GroupResponses[group]
			if !available && !retained {
				userModelRateLimitAPIError(c, http.StatusBadRequest, "group is not currently available: "+group)
				return
			}
		}
		groupResponses[group] = dto.ModelRateLimitResponse{
			StatusCode:   item.StatusCode,
			ErrorMessage: item.ErrorMessage,
		}
	}

	normalized, err := setting.NormalizeUserModelRateLimitResponseConfig(dto.UserModelRateLimitResponseConfig{
		DelaySeconds:    request.DelaySeconds,
		DefaultResponse: request.DefaultResponse,
		GroupResponses:  groupResponses,
	})
	if err != nil {
		userModelRateLimitAPIError(c, http.StatusBadRequest, err.Error())
		return
	}
	jsonBytes, err := common.Marshal(normalized)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOption(setting.UserModelRateLimitResponseConfigOption, string(jsonBytes)); err != nil {
		common.ApiError(c, err)
		return
	}

	auditGroups := make([]map[string]interface{}, 0, len(normalized.GroupResponses))
	for _, item := range sortedUserModelRateLimitGroupResponses(normalized.GroupResponses) {
		auditGroups = append(auditGroups, map[string]interface{}{
			"group":       item.Group,
			"status_code": item.StatusCode,
		})
	}
	recordManageAudit(c, "user_rate_limit.config_update", map[string]interface{}{
		"delay_seconds":       normalized.DelaySeconds,
		"default_status_code": normalized.DefaultResponse.StatusCode,
		"group_responses":     auditGroups,
	})
	GetUserModelRateLimitConfig(c)
}

// GetUserModelRateLimitRules returns the searchable and paginated rule table.
func GetUserModelRateLimitRules(c *gin.Context) {
	page := parsePositiveQueryInt(c.Query("page"), parsePositiveQueryInt(c.Query("p"), 1))
	pageSize := parsePositiveQueryInt(c.Query("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	rows, total, err := model.SearchUserModelRateLimits(c.Query("keyword"), c.Query("group"), (page-1)*pageSize, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]userModelRateLimitRuleView, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildUserModelRateLimitRuleView(row))
	}
	common.ApiSuccess(c, gin.H{
		"page":      page,
		"page_size": pageSize,
		"total":     total,
		"items":     items,
	})
}

// CreateUserModelRateLimitRule validates and persists one user-group rule.
func CreateUserModelRateLimitRule(c *gin.Context) {
	var request dto.UserModelRateLimitRuleRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		userModelRateLimitAPIError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	rule, err := normalizeUserModelRateLimitRuleRequest(request)
	if err != nil {
		userModelRateLimitAPIError(c, http.StatusBadRequest, err.Error())
		return
	}
	user, err := model.GetUserById(rule.UserId, false)
	if err != nil {
		userModelRateLimitAPIError(c, http.StatusNotFound, "user not found")
		return
	}
	if err := validateUserModelRateLimitGroup(user, rule.GroupName); err != nil {
		userModelRateLimitAPIError(c, http.StatusBadRequest, err.Error())
		return
	}
	exists, err := model.UserModelRateLimitExists(rule.UserId, rule.GroupName, 0)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if exists {
		userModelRateLimitAPIError(c, http.StatusConflict, "a rule already exists for this user and group")
		return
	}
	if err := model.CreateUserModelRateLimit(rule); err != nil {
		if duplicate, checkErr := model.UserModelRateLimitExists(rule.UserId, rule.GroupName, 0); checkErr == nil && duplicate {
			userModelRateLimitAPIError(c, http.StatusConflict, "a rule already exists for this user and group")
			return
		}
		common.ApiError(c, err)
		return
	}
	recordUserModelRateLimitRuleAudit(c, rule, "user_rate_limit.rule_create")
	common.ApiSuccess(c, buildUserModelRateLimitRuleViewFromRule(*rule, *user))
}

// UpdateUserModelRateLimitRule updates mutable fields while retaining the original owner.
func UpdateUserModelRateLimitRule(c *gin.Context) {
	ruleId, err := strconv.Atoi(c.Param("id"))
	if err != nil || ruleId <= 0 {
		userModelRateLimitAPIError(c, http.StatusBadRequest, "invalid rule id")
		return
	}
	existing, err := model.GetUserModelRateLimitById(ruleId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		userModelRateLimitAPIError(c, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var request dto.UserModelRateLimitRuleRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		userModelRateLimitAPIError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	request.UserId = existing.UserId
	updated, err := normalizeUserModelRateLimitRuleRequest(request)
	if err != nil {
		userModelRateLimitAPIError(c, http.StatusBadRequest, err.Error())
		return
	}
	user, err := model.GetUserById(existing.UserId, false)
	if err != nil {
		userModelRateLimitAPIError(c, http.StatusNotFound, "user not found")
		return
	}
	if updated.GroupName != existing.GroupName {
		if err := validateUserModelRateLimitGroup(user, updated.GroupName); err != nil {
			userModelRateLimitAPIError(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	exists, err := model.UserModelRateLimitExists(existing.UserId, updated.GroupName, existing.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if exists {
		userModelRateLimitAPIError(c, http.StatusConflict, "a rule already exists for this user and group")
		return
	}
	updated.Id = existing.Id
	updated.CreatedAt = existing.CreatedAt
	if err := model.UpdateUserModelRateLimit(updated); err != nil {
		common.ApiError(c, err)
		return
	}
	recordUserModelRateLimitRuleAudit(c, updated, "user_rate_limit.rule_update")
	common.ApiSuccess(c, buildUserModelRateLimitRuleViewFromRule(*updated, *user))
}

// DeleteUserModelRateLimitRule removes one rule without resetting its rate-limit counters.
func DeleteUserModelRateLimitRule(c *gin.Context) {
	ruleId, err := strconv.Atoi(c.Param("id"))
	if err != nil || ruleId <= 0 {
		userModelRateLimitAPIError(c, http.StatusBadRequest, "invalid rule id")
		return
	}
	rule, err := model.GetUserModelRateLimitById(ruleId)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		userModelRateLimitAPIError(c, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DeleteUserModelRateLimit(rule); err != nil {
		common.ApiError(c, err)
		return
	}
	recordUserModelRateLimitRuleAudit(c, rule, "user_rate_limit.rule_delete")
	common.ApiSuccess(c, nil)
}

// normalizeUserModelRateLimitRuleRequest validates and canonicalizes a rule payload.
func normalizeUserModelRateLimitRuleRequest(request dto.UserModelRateLimitRuleRequest) (*model.UserModelRateLimit, error) {
	if request.UserId <= 0 {
		return nil, errors.New("user_id must be positive")
	}
	group := strings.TrimSpace(request.Group)
	if len(group) < 1 || len(group) > 64 {
		return nil, errors.New("group length must be between 1 and 64")
	}
	if request.TotalCount < 0 || request.TotalCount > setting.UserModelRateLimitMaxCount {
		return nil, errors.New("total_count must be between 0 and 100000000")
	}
	if request.SuccessCount < 1 || request.SuccessCount > setting.UserModelRateLimitMaxCount {
		return nil, errors.New("success_count must be between 1 and 100000000")
	}
	rule := &model.UserModelRateLimit{
		UserId:       request.UserId,
		GroupName:    group,
		TotalCount:   request.TotalCount,
		SuccessCount: request.SuccessCount,
	}
	if request.Response != nil {
		response, err := setting.NormalizeModelRateLimitResponse(*request.Response)
		if err != nil {
			return nil, err
		}
		rule.StatusCode = common.GetPointer(response.StatusCode)
		rule.ErrorMessage = common.GetPointer(response.ErrorMessage)
	}
	return rule, nil
}

// validateUserModelRateLimitGroup enforces target-user group availability on create or group change.
func validateUserModelRateLimitGroup(user *model.User, group string) error {
	if group == "auto" {
		return nil
	}
	available, err := service.GroupInUserEffectiveGroups(user.Id, user.Group, group)
	if err != nil {
		return err
	}
	if !available {
		return errors.New("group is not currently available to this user")
	}
	return nil
}

// buildUserModelRateLimitRuleView maps a joined management row into the public API contract.
func buildUserModelRateLimitRuleView(row model.UserModelRateLimitWithUser) userModelRateLimitRuleView {
	user := model.User{
		Id:          row.UserId,
		Username:    row.Username,
		DisplayName: row.DisplayName,
		Email:       row.Email,
		Status:      row.UserStatus,
	}
	return buildUserModelRateLimitRuleViewFromRule(row.UserModelRateLimit, user)
}

// buildUserModelRateLimitRuleViewFromRule resolves the current response fallback for one rule.
func buildUserModelRateLimitRuleViewFromRule(rule model.UserModelRateLimit, user model.User) userModelRateLimitRuleView {
	var response *dto.ModelRateLimitResponse
	if rule.StatusCode != nil && rule.ErrorMessage != nil {
		response = &dto.ModelRateLimitResponse{StatusCode: *rule.StatusCode, ErrorMessage: *rule.ErrorMessage}
	}
	effective, source, _ := setting.ResolveUserModelRateLimitResponse(rule.GroupName, rule.StatusCode, rule.ErrorMessage)
	return userModelRateLimitRuleView{
		Id: rule.Id,
		User: userModelRateLimitUserView{
			Id:          user.Id,
			Username:    user.Username,
			DisplayName: user.DisplayName,
			Email:       user.Email,
			Status:      user.Status,
		},
		Group:        rule.GroupName,
		TotalCount:   rule.TotalCount,
		SuccessCount: rule.SuccessCount,
		Response:     response,
		EffectiveResponse: userModelRateLimitEffectiveResponse{
			ModelRateLimitResponse: effective,
			Source:                 source,
		},
		CreatedAt: rule.CreatedAt,
		UpdatedAt: rule.UpdatedAt,
	}
}

// sortedUserModelRateLimitGroupResponses returns stable API and audit ordering.
func sortedUserModelRateLimitGroupResponses(responses map[string]dto.ModelRateLimitResponse) []userModelRateLimitGroupResponse {
	groups := make([]string, 0, len(responses))
	for group := range responses {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	items := make([]userModelRateLimitGroupResponse, 0, len(groups))
	for _, group := range groups {
		response := responses[group]
		items = append(items, userModelRateLimitGroupResponse{
			Group:        group,
			StatusCode:   response.StatusCode,
			ErrorMessage: response.ErrorMessage,
		})
	}
	return items
}

// recordUserModelRateLimitRuleAudit records structured fields without copying the public error text.
func recordUserModelRateLimitRuleAudit(c *gin.Context, rule *model.UserModelRateLimit, action string) {
	params := map[string]interface{}{
		"rule_id":               rule.Id,
		"group":                 rule.GroupName,
		"total_count":           rule.TotalCount,
		"success_count":         rule.SuccessCount,
		"has_response_override": rule.StatusCode != nil,
	}
	if rule.StatusCode != nil {
		params["status_code"] = *rule.StatusCode
	}
	recordManageAuditFor(c, rule.UserId, action, params)
}

// userModelRateLimitAPIError returns an HTTP-level management API error.
func userModelRateLimitAPIError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"success": false, "message": message})
}

// parsePositiveQueryInt parses one positive pagination value with a stable fallback.
func parsePositiveQueryInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
