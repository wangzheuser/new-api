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
	"github.com/QuantumNous/new-api/service"

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
	if hasActiveDashboardSession(c) {
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

	bundle, err := service.CreateLoginSession(
		targetUser.Id,
		"impersonation",
		c.ClientIP(),
		c.Request.UserAgent(),
	)
	if err != nil {
		respondUserImpersonationError(c, "impersonation_server_error", i18n.MsgUserSessionSaveFailed)
		return
	}
	service.WriteRefreshCookie(c, bundle.RefreshToken)

	params := map[string]any{
		"target_user_id":  targetUser.Id,
		"target_username": targetUser.Username,
		"target_role":     targetUser.Role,
	}
	model.RecordOperationAuditLog(adminUser.Id, adminUser.Role, auditContentEN("user.impersonation_redeem", params), c.ClientIP(), "user.impersonation_redeem", params,
		&model.AuditAdminInfo{AdminID: adminUser.Id, AdminUsername: adminUser.Username, AdminRole: adminUser.Role, AuthMethod: "impersonation_ticket"},
		&model.AuditRequestInfo{Method: c.Request.Method, Route: c.FullPath(), Path: c.Request.URL.Path, Status: http.StatusOK, Success: true}, c)

	common.ApiSuccess(c, gin.H{
		"access_token":      bundle.AccessToken,
		"token_type":        bundle.TokenType,
		"access_expires_at": bundle.AccessExpiresAt,
		"session":           bundle.Session,
		"user":              buildSelfUserData(targetUser),
	})
}

// hasActiveDashboardSession rejects redemption in a browser that already has
// a live token session, without depending on the removed Gin session cookie.
func hasActiveDashboardSession(c *gin.Context) bool {
	if rawAccessToken, ok := dashboardBearer(c.GetHeader("Authorization")); ok {
		identity, err := service.ParseAccessToken(rawAccessToken)
		if err == nil {
			if _, _, err := service.ValidateLoginSession(identity); err == nil {
				return true
			}
		}
	}

	rawRefreshToken, err := c.Cookie(service.RefreshCookieName)
	if err != nil || rawRefreshToken == "" {
		return false
	}
	sessionID, ok := service.RefreshTokenSID(rawRefreshToken)
	if !ok {
		return false
	}
	session, err := model.GetUserSessionCached(sessionID)
	if err != nil || session.Status != model.UserSessionStatusActive ||
		session.RevokedAt != 0 || session.ExpiresAt <= time.Now().Unix() {
		return false
	}
	user, err := model.GetUserCache(session.UserID)
	return err == nil && user.Status == common.UserStatusEnabled &&
		user.AuthVersion == session.UserAuthVersion
}
