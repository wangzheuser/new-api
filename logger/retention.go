package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/bytedance/gopkg/util/gopool"
)

const (
	// CleanupModeByCount 按数量保留最新日志文件。
	CleanupModeByCount = "by_count"
	// CleanupModeByDays 删除超过指定天数的日志文件。
	CleanupModeByDays = "by_days"
)

// LogFileInfo 描述一个受管服务器日志文件。
type LogFileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// LogCleanupResult 描述一次服务器日志清理结果。
type LogCleanupResult struct {
	DeletedCount   int      `json:"deleted_count"`
	FreedBytes     int64    `json:"freed_bytes"`
	FailedFiles    []string `json:"failed_files"`
	AttemptedCount int      `json:"-"`
}

var logRetentionTaskOnce sync.Once

// ListLogFiles 按从新到旧顺序返回受管普通日志文件。
func ListLogFiles(logDir string) ([]LogFileInfo, error) {
	if logDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return nil, err
	}

	var files []LogFileInfo
	for _, entry := range entries {
		// 日志维护只处理当前目录中的普通文件，避免跟随符号链接或进入子目录。
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isManagedLogName(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, LogFileInfo{
			Name:    entry.Name(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name > files[j].Name
	})
	return files, nil
}

// CleanupLogFiles 按指定模式清理受管日志文件。
func CleanupLogFiles(logDir string, mode string, value int, now time.Time) (LogCleanupResult, error) {
	return cleanupLogFiles(logDir, mode, value, now, GetCurrentLogPath(), os.Remove)
}

// cleanupLogFiles 使用可注入状态执行一次清理，便于确定性测试。
func cleanupLogFiles(
	logDir string,
	mode string,
	value int,
	now time.Time,
	activeLogPath string,
	removeFile func(string) error,
) (LogCleanupResult, error) {
	result := LogCleanupResult{}
	if mode != CleanupModeByCount && mode != CleanupModeByDays {
		return result, fmt.Errorf("invalid cleanup mode: %s", mode)
	}
	if value < 1 {
		return result, fmt.Errorf("cleanup value must be positive")
	}

	files, err := ListLogFiles(logDir)
	if err != nil {
		return result, err
	}
	activeLogPath = filepath.Clean(activeLogPath)
	cutoff := now.AddDate(0, 0, -value)
	for index, file := range files {
		fullPath := filepath.Join(logDir, file.Name)
		if filepath.Clean(fullPath) == activeLogPath {
			continue
		}

		shouldDelete := mode == CleanupModeByCount && index >= value
		if mode == CleanupModeByDays {
			shouldDelete = file.ModTime.Before(cutoff)
		}
		if !shouldDelete {
			continue
		}
		result.AttemptedCount++
		if removeErr := removeFile(fullPath); removeErr != nil {
			result.FailedFiles = append(result.FailedFiles, file.Name)
			continue
		}
		result.DeletedCount++
		result.FreedBytes += file.Size
	}
	return result, nil
}

// StartLogRetentionTask 启动每小时执行一次的单例日志保留任务。
func StartLogRetentionTask() {
	logRetentionTaskOnce.Do(func() {
		gopool.Go(func() {
			runLogRetention(time.Now())
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for now := range ticker.C {
				runLogRetention(now)
			}
		})
	})
}

// runLogRetention 使用最新同步配置删除过期文件。
func runLogRetention(now time.Time) {
	days := common.GetServerLogRetentionDays()
	if days <= 0 || common.LogDir == nil || *common.LogDir == "" {
		return
	}
	result, err := CleanupLogFiles(*common.LogDir, CleanupModeByDays, days, now)
	if err != nil {
		LogWarn(nil, "automatic server log cleanup failed: "+err.Error())
		return
	}
	for _, name := range result.FailedFiles {
		LogWarn(nil, "failed to remove expired server log: "+name)
	}
	if result.DeletedCount > 0 {
		LogInfo(nil, fmt.Sprintf(
			"automatic server log cleanup: deleted_count=%d freed_bytes=%d retention_days=%d",
			result.DeletedCount,
			result.FreedBytes,
			days,
		))
	}
}

// isManagedLogName 将清理范围限制为 SetupLogger 创建的文件。
func isManagedLogName(name string) bool {
	return strings.HasPrefix(name, "oneapi-") && strings.HasSuffix(name, ".log")
}
