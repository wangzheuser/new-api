package model

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

// ConversationLog stores one bounded final relay attempt.
type ConversationLog struct {
	Id                        int    `json:"id" gorm:"index:idx_conversation_logs_created_id,priority:2"`
	CreatedAt                 int64  `json:"created_at" gorm:"bigint;index:idx_conversation_logs_created_id,priority:1"`
	RequestId                 string `json:"request_id" gorm:"type:varchar(64);index"`
	UserId                    int    `json:"user_id" gorm:"index"`
	Username                  string `json:"username" gorm:"type:varchar(64);index"`
	TokenId                   int    `json:"token_id" gorm:"index"`
	ChannelId                 int    `json:"channel_id" gorm:"index"`
	Group                     string `json:"group" gorm:"column:group;type:varchar(64);index"`
	ModelName                 string `json:"model_name" gorm:"type:varchar(128);index"`
	UpstreamModelName         string `json:"upstream_model_name" gorm:"type:varchar(128);index"`
	RelayFormat               string `json:"relay_format" gorm:"type:varchar(64)"`
	RequestPath               string `json:"request_path" gorm:"type:varchar(255)"`
	IsStream                  bool   `json:"is_stream"`
	StatusCode                int    `json:"status_code"`
	ClientRequestBody         string `json:"client_request_body,omitempty" gorm:"type:text"`
	UpstreamRequestBody       string `json:"upstream_request_body,omitempty" gorm:"type:text"`
	UpstreamResponseBody      string `json:"upstream_response_body,omitempty" gorm:"type:text"`
	ClientResponseBody        string `json:"client_response_body,omitempty" gorm:"type:text"`
	Metadata                  string `json:"metadata,omitempty" gorm:"type:text"`
	StorageBytes              int64  `json:"storage_bytes" gorm:"bigint;index"`
	ClientRequestTruncated    bool   `json:"client_request_truncated"`
	UpstreamRequestTruncated  bool   `json:"upstream_request_truncated"`
	UpstreamResponseTruncated bool   `json:"upstream_response_truncated"`
	ClientResponseTruncated   bool   `json:"client_response_truncated"`
}

// ConversationLogQuery contains supported root-side filters.
type ConversationLogQuery struct {
	StartTime int64
	EndTime   int64
	UserId    int
	ChannelId int
	ModelName string
	RequestId string
	Group     string
}

// ConversationLogSummary reports current storage use.
type ConversationLogSummary struct {
	StorageBytes int64 `json:"storage_bytes"`
	RecordCount  int64 `json:"record_count"`
}

// applyConversationLogQuery applies the shared root-side filters.
func applyConversationLogQuery(db *gorm.DB, query ConversationLogQuery) *gorm.DB {
	if query.StartTime > 0 {
		db = db.Where("created_at >= ?", query.StartTime)
	}
	if query.EndTime > 0 {
		db = db.Where("created_at <= ?", query.EndTime)
	}
	if query.UserId > 0 {
		db = db.Where("user_id = ?", query.UserId)
	}
	if query.ChannelId > 0 {
		db = db.Where("channel_id = ?", query.ChannelId)
	}
	if query.ModelName != "" {
		db = db.Where("model_name = ?", query.ModelName)
	}
	if query.RequestId != "" {
		db = db.Where("request_id = ?", query.RequestId)
	}
	if query.Group != "" {
		db = db.Where(logGroupCol+" = ?", query.Group)
	}
	return db
}

// CreateConversationLog persists one captured relay attempt.
func CreateConversationLog(log *ConversationLog) error {
	return LOG_DB.Create(log).Error
}

// GetConversationLogs returns metadata-only rows for a page.
func GetConversationLogs(query ConversationLogQuery, startIdx int, num int) ([]*ConversationLog, int64, error) {
	base := applyConversationLogQuery(LOG_DB.Model(&ConversationLog{}), query)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []*ConversationLog
	err := applyConversationLogQuery(LOG_DB.Model(&ConversationLog{}), query).
		Select("id, created_at, request_id, user_id, username, token_id, channel_id, " + logGroupCol + ", model_name, upstream_model_name, relay_format, request_path, is_stream, status_code, storage_bytes, client_request_truncated, upstream_request_truncated, upstream_response_truncated, client_response_truncated").
		Order("created_at desc, id desc").Offset(startIdx).Limit(num).Find(&logs).Error
	return logs, total, err
}

// GetConversationLogById returns a complete captured record.
func GetConversationLogById(id int) (*ConversationLog, error) {
	var log ConversationLog
	if err := LOG_DB.First(&log, id).Error; err != nil {
		return nil, err
	}
	return &log, nil
}

// GetConversationLogSummary aggregates record count and stored bytes.
func GetConversationLogSummary() (ConversationLogSummary, error) {
	summary := ConversationLogSummary{}
	if err := LOG_DB.Model(&ConversationLog{}).Count(&summary.RecordCount).Error; err != nil {
		return summary, err
	}
	var storage sql.NullInt64
	err := LOG_DB.Model(&ConversationLog{}).Select("COALESCE(SUM(storage_bytes), 0)").Scan(&storage).Error
	summary.StorageBytes = storage.Int64
	return summary, err
}

// ForEachConversationLog iterates matching rows in stable ID order.
func ForEachConversationLog(ctx context.Context, query ConversationLogQuery, batchSize int, fn func([]*ConversationLog) error) error {
	if batchSize <= 0 {
		batchSize = 100
	}
	lastId := 0
	for {
		var logs []*ConversationLog
		db := LOG_DB.WithContext(ctx).Model(&ConversationLog{})
		err := applyConversationLogQuery(db, query).Where("id > ?", lastId).Order("id asc").Limit(batchSize).Find(&logs).Error
		if err != nil || len(logs) == 0 {
			return err
		}
		if err := fn(logs); err != nil {
			return err
		}
		lastId = logs[len(logs)-1].Id
	}
}

// DeleteConversationLogs deletes matching rows in bounded batches.
func DeleteConversationLogs(ctx context.Context, query ConversationLogQuery, batchSize int) (int64, error) {
	var deleted int64
	err := ForEachConversationLog(ctx, query, batchSize, func(logs []*ConversationLog) error {
		ids := make([]int, 0, len(logs))
		for _, log := range logs {
			ids = append(ids, log.Id)
		}
		result := LOG_DB.WithContext(ctx).Where("id IN ?", ids).Delete(&ConversationLog{})
		deleted += result.RowsAffected
		return result.Error
	})
	return deleted, err
}

// TrimConversationLogs removes oldest rows until storage is within maxBytes.
func TrimConversationLogs(ctx context.Context, maxBytes int64, batchSize int) (int64, error) {
	if maxBytes <= 0 {
		return 0, nil
	}
	summary, err := GetConversationLogSummary()
	if err != nil || summary.StorageBytes <= maxBytes {
		return 0, err
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	var deleted int64
	for summary.StorageBytes > maxBytes {
		var logs []*ConversationLog
		if err := LOG_DB.WithContext(ctx).Select("id, storage_bytes").Order("created_at asc, id asc").Limit(batchSize).Find(&logs).Error; err != nil {
			return deleted, err
		}
		if len(logs) == 0 {
			return deleted, nil
		}
		ids := make([]int, 0, len(logs))
		for _, log := range logs {
			ids = append(ids, log.Id)
			summary.StorageBytes -= log.StorageBytes
			if summary.StorageBytes <= maxBytes {
				break
			}
		}
		result := LOG_DB.WithContext(ctx).Where("id IN ?", ids).Delete(&ConversationLog{})
		if result.Error != nil {
			return deleted, result.Error
		}
		deleted += result.RowsAffected
	}
	return deleted, nil
}
