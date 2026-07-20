package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// ensureConversationLogSupported rejects unsupported ClickHouse log storage.
func ensureConversationLogSupported(c *gin.Context) bool {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		common.ApiError(c, errors.New("conversation capture does not support ClickHouse LOG_DB"))
		return false
	}
	return true
}

// parseConversationLogQuery converts request filters to a model query.
func parseConversationLogQuery(c *gin.Context) model.ConversationLogQuery {
	startTime, _ := strconv.ParseInt(c.Query("start_time"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end_time"), 10, 64)
	userId, _ := strconv.Atoi(c.Query("user_id"))
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	return model.ConversationLogQuery{
		StartTime: startTime,
		EndTime:   endTime,
		UserId:    userId,
		ChannelId: channelId,
		ModelName: c.Query("model_name"),
		RequestId: c.Query("request_id"),
		Group:     c.Query("group"),
	}
}

// GetConversationLogs returns a metadata-only page of captured conversations.
func GetConversationLogs(c *gin.Context) {
	if !ensureConversationLogSupported(c) {
		return
	}
	pageInfo := common.GetPageQuery(c)
	logs, total, err := model.GetConversationLogs(parseConversationLogQuery(c), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// GetConversationLog returns one complete captured conversation.
func GetConversationLog(c *gin.Context) {
	if !ensureConversationLogSupported(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiError(c, errors.New("invalid conversation log id"))
		return
	}
	log, err := model.GetConversationLogById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, log)
}

// GetConversationLogSummary returns storage usage and current settings.
func GetConversationLogSummary(c *gin.Context) {
	if !ensureConversationLogSupported(c) {
		return
	}
	summary, err := model.GetConversationLogSummary()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"summary": summary,
		"settings": gin.H{
			"enabled":        common.ConversationCaptureEnabled,
			"retention_days": common.ConversationLogRetentionDays,
			"max_storage_gb": common.ConversationLogMaxStorageGB,
		},
	})
}

// UpdateConversationLogSettings updates the global capture and cleanup controls.
func UpdateConversationLogSettings(c *gin.Context) {
	var req struct {
		Enabled       *bool `json:"enabled"`
		RetentionDays *int  `json:"retention_days"`
		MaxStorageGB  *int  `json:"max_storage_gb"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	values := map[string]string{}
	if req.Enabled != nil {
		if *req.Enabled && common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			common.ApiError(c, errors.New("conversation capture does not support ClickHouse LOG_DB"))
			return
		}
		values["ConversationCaptureEnabled"] = strconv.FormatBool(*req.Enabled)
	}
	if req.RetentionDays != nil {
		if *req.RetentionDays < 0 || *req.RetentionDays > 3650 {
			common.ApiError(c, errors.New("retention_days must be between 0 and 3650"))
			return
		}
		values["ConversationLogRetentionDays"] = strconv.Itoa(*req.RetentionDays)
	}
	if req.MaxStorageGB != nil {
		if *req.MaxStorageGB < 0 || *req.MaxStorageGB > 10240 {
			common.ApiError(c, errors.New("max_storage_gb must be between 0 and 10240"))
			return
		}
		values["ConversationLogMaxStorageGB"] = strconv.Itoa(*req.MaxStorageGB)
	}
	if err := model.UpdateOptionsBulk(values); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "conversation_log.config", map[string]interface{}{"values": values})
	common.ApiSuccess(c, nil)
}

// DeleteConversationLogs deletes records matching query filters.
func DeleteConversationLogs(c *gin.Context) {
	if !ensureConversationLogSupported(c) {
		return
	}
	deleted, err := model.DeleteConversationLogs(c.Request.Context(), parseConversationLogQuery(c), 200)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "conversation_log.delete", map[string]interface{}{"deleted": deleted})
	common.ApiSuccess(c, gin.H{"deleted": deleted})
}

// CleanupConversationLogs applies configured retention and storage limits immediately.
func CleanupConversationLogs(c *gin.Context) {
	if !ensureConversationLogSupported(c) {
		return
	}
	result, err := service.CleanupConversationLogs(c.Request.Context())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "conversation_log.cleanup", map[string]interface{}{
		"expired": result["expired"],
		"trimmed": result["trimmed"],
	})
	common.ApiSuccess(c, result)
}

// ExportConversationLogs streams matching records as JSONL.
func ExportConversationLogs(c *gin.Context) {
	if !ensureConversationLogSupported(c) {
		return
	}
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Content-Disposition", "attachment; filename=conversation-logs.jsonl")
	c.Status(http.StatusOK)
	err := model.ForEachConversationLog(c.Request.Context(), parseConversationLogQuery(c), 100, func(logs []*model.ConversationLog) error {
		for _, log := range logs {
			data, err := common.Marshal(log)
			if err != nil {
				return err
			}
			if _, err := c.Writer.Write(append(data, '\n')); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		common.SysError("failed to export conversation logs: " + err.Error())
	}
}
