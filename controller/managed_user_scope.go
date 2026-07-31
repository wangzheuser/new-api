package controller

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// resolveManagedUserID 解析可选的 user_id，并校验当前操作者是否有权管理目标用户。
func resolveManagedUserID(c *gin.Context) (int, error) {
	operatorUserId := c.GetInt("id")
	targetUserIdText := c.Query("user_id")
	if targetUserIdText == "" {
		return operatorUserId, nil
	}

	targetUserId, err := strconv.Atoi(targetUserIdText)
	if err != nil || targetUserId <= 0 {
		return 0, errors.New("无效的用户 ID")
	}
	if targetUserId == operatorUserId {
		return targetUserId, nil
	}
	if c.GetInt("role") < common.RoleAdminUser {
		return 0, errors.New("无权管理其他用户")
	}

	targetUser, err := model.GetUserById(targetUserId, false)
	if err != nil {
		return 0, err
	}
	if !canManageTargetRole(c.GetInt("role"), targetUser.Role) {
		return 0, errors.New("无权管理同级或更高级用户")
	}
	return targetUserId, nil
}

// recordDelegatedTokenAudit 仅为管理员代管其他用户密钥的敏感操作记录审计日志。
func recordDelegatedTokenAudit(c *gin.Context, targetUserId int, action string, params map[string]interface{}) {
	if targetUserId == c.GetInt("id") {
		return
	}
	recordManageAuditFor(c, targetUserId, action, params)
}
