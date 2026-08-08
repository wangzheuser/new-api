package controller

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
)

const (
	userImpersonationTicketTTL    = 5 * time.Minute
	userImpersonationTicketPrefix = "user:impersonation:"
	userImpersonationTicketMaxLen = 128
)

type userImpersonationTicket struct {
	AdminUserId  int   `json:"admin_user_id"`
	TargetUserId int   `json:"target_user_id"`
	IssuedAt     int64 `json:"issued_at"`
}

type redeemUserImpersonationTicketRequest struct {
	Ticket string `json:"ticket"`
}

// userImpersonationTicketKey derives the Redis key without persisting the bearer ticket.
func userImpersonationTicketKey(ticket string) string {
	return fmt.Sprintf("%s%x", userImpersonationTicketPrefix, sha256.Sum256([]byte(ticket)))
}

// respondUserImpersonationError returns a stable machine code and a localized message.
func respondUserImpersonationError(c *gin.Context, code string, messageKey string) {
	c.JSON(http.StatusOK, gin.H{
		"success": false,
		"code":    code,
		"message": common.TranslateMessage(c, messageKey),
	})
}

// CreateUserImpersonationTicket creates a five-minute, single-use ticket for an enabled common user.
func CreateUserImpersonationTicket(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !common.RedisEnabled || common.RDB == nil {
		respondUserImpersonationError(c, "impersonation_server_error", i18n.MsgUserImpersonationRedisRequired)
		return
	}

	targetUserId, err := strconv.Atoi(c.Param("id"))
	if err != nil || targetUserId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidId)
		return
	}

	targetUser, err := model.GetUserById(targetUserId, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorI18n(c, i18n.MsgUserNotExists)
		} else {
			common.ApiErrorI18n(c, i18n.MsgDatabaseError)
		}
		return
	}
	if targetUser.Status != common.UserStatusEnabled || targetUser.Role != common.RoleCommonUser ||
		!canManageTargetRole(c.GetInt("role"), targetUser.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserImpersonationTargetInvalid)
		return
	}

	ticket, err := common.GenerateRandomKey(48)
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgGenerateFailed)
		return
	}
	now := time.Now()
	ticketData, err := common.Marshal(userImpersonationTicket{
		AdminUserId:  c.GetInt("id"),
		TargetUserId: targetUser.Id,
		IssuedAt:     now.Unix(),
	})
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgGenerateFailed)
		return
	}

	written, err := common.RedisSetNX(userImpersonationTicketKey(ticket), string(ticketData), userImpersonationTicketTTL)
	if err != nil || !written {
		common.ApiErrorI18n(c, i18n.MsgRetryLater)
		return
	}

	recordManageAuditFor(c, targetUser.Id, "user.impersonation_link_create", map[string]interface{}{
		"target_username": targetUser.Username,
		"target_role":     targetUser.Role,
		"ttl_seconds":     int(userImpersonationTicketTTL.Seconds()),
	})
	common.ApiSuccess(c, gin.H{
		"ticket":     ticket,
		"expires_at": now.Add(userImpersonationTicketTTL).Unix(),
	})
}

// RedeemUserImpersonationTicket consumes a ticket and creates the target user's session without login side effects.
func RedeemUserImpersonationTicket(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	session := sessions.Default(c)
	if session.Get("id") != nil {
		respondUserImpersonationError(c, "impersonation_existing_session", i18n.MsgUserImpersonationExistingSession)
		return
	}
	if !common.RedisEnabled || common.RDB == nil {
		respondUserImpersonationError(c, "impersonation_server_error", i18n.MsgUserImpersonationRedisRequired)
		return
	}

	var request redeemUserImpersonationTicketRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		respondUserImpersonationError(c, "impersonation_ticket_invalid", i18n.MsgUserImpersonationTicketInvalid)
		return
	}
	request.Ticket = strings.TrimSpace(request.Ticket)
	if request.Ticket == "" || len(request.Ticket) > userImpersonationTicketMaxLen {
		respondUserImpersonationError(c, "impersonation_ticket_invalid", i18n.MsgUserImpersonationTicketInvalid)
		return
	}

	redisKey := userImpersonationTicketKey(request.Ticket)
	value, err := common.RedisGetDel(redisKey)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			respondUserImpersonationError(c, "impersonation_ticket_invalid", i18n.MsgUserImpersonationTicketInvalid)
		} else {
			respondUserImpersonationError(c, "impersonation_server_error", i18n.MsgRetryLater)
		}
		return
	}

	var ticketData userImpersonationTicket
	if err := common.UnmarshalJsonStr(value, &ticketData); err != nil {
		common.SysError(fmt.Sprintf("invalid impersonation ticket data: key=%s", redisKey))
		respondUserImpersonationError(c, "impersonation_ticket_invalid", i18n.MsgUserImpersonationTicketInvalid)
		return
	}

	adminUser, err := model.GetUserById(ticketData.AdminUserId, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondUserImpersonationError(c, "impersonation_ticket_invalid", i18n.MsgUserImpersonationTicketInvalid)
		} else {
			respondUserImpersonationError(c, "impersonation_server_error", i18n.MsgDatabaseError)
		}
		return
	}
	targetUser, err := model.GetUserById(ticketData.TargetUserId, false)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondUserImpersonationError(c, "impersonation_ticket_invalid", i18n.MsgUserImpersonationTicketInvalid)
		} else {
			respondUserImpersonationError(c, "impersonation_server_error", i18n.MsgDatabaseError)
		}
		return
	}
	if adminUser.Status != common.UserStatusEnabled || adminUser.Role < common.RoleAdminUser ||
		targetUser.Status != common.UserStatusEnabled || targetUser.Role != common.RoleCommonUser ||
		!canManageTargetRole(adminUser.Role, targetUser.Role) {
		respondUserImpersonationError(c, "impersonation_ticket_invalid", i18n.MsgUserImpersonationTicketInvalid)
		return
	}

	// Write only the identity fields used by the existing session authentication middleware.
	session.Clear()
	session.Set("id", targetUser.Id)
	session.Set("username", targetUser.Username)
	session.Set("role", targetUser.Role)
	session.Set("status", targetUser.Status)
	session.Set("group", targetUser.Group)
	if err := session.Save(); err != nil {
		respondUserImpersonationError(c, "impersonation_server_error", i18n.MsgUserSessionSaveFailed)
		return
	}

	params := map[string]interface{}{
		"target_user_id":  targetUser.Id,
		"target_username": targetUser.Username,
		"target_role":     targetUser.Role,
	}
	model.RecordOperationAuditLog(adminUser.Id, auditContentEN("user.impersonation_redeem", params), c.ClientIP(), "user.impersonation_redeem", params, map[string]interface{}{
		"admin_id":       adminUser.Id,
		"admin_username": adminUser.Username,
		"admin_role":     adminUser.Role,
		"auth_method":    "impersonation_ticket",
	}, map[string]interface{}{
		"method":     c.Request.Method,
		"route":      c.FullPath(),
		"path":       c.Request.URL.Path,
		"status":     http.StatusOK,
		"success":    true,
		"request_id": c.GetString(common.RequestIdKey),
		"client_ip":  c.ClientIP(),
		"user_agent": c.Request.UserAgent(),
	})

	common.ApiSuccess(c, gin.H{
		"id":           targetUser.Id,
		"username":     targetUser.Username,
		"display_name": targetUser.DisplayName,
		"role":         targetUser.Role,
		"status":       targetUser.Status,
		"group":        targetUser.Group,
	})
}
