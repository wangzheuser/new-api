package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupPerformanceOptionTest isolates option persistence and synchronized settings.
func setupPerformanceOptionTest(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousSetting := *performance_setting.GetPerformanceSetting()
	common.OptionMapRWMutex.Lock()
	previousOptionMap := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	dsn := fmt.Sprintf("file:performance-option-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		*performance_setting.GetPerformanceSetting() = previousSetting
		performance_setting.UpdateAndSync()
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptionMap
		common.OptionMapRWMutex.Unlock()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// updatePerformanceOption invokes the generic settings handler with JSON input.
func updatePerformanceOption(t *testing.T, key string, value any) (int, map[string]interface{}) {
	t.Helper()
	body, err := common.Marshal(map[string]interface{}{"key": key, "value": value})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/option", bytes.NewReader(body))
	UpdateOption(ctx)
	var response map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder.Code, response
}

func TestUpdateOptionValidatesAndSynchronizesPerformanceSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupPerformanceOptionTest(t)

	for _, days := range []int{0, 7, 3650} {
		status, response := updatePerformanceOption(t, "performance_setting.server_log_retention_days", days)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, true, response["success"])
		assert.Equal(t, days, common.GetServerLogRetentionDays())
	}
	for _, scope := range []string{common.ResourceScopeHost, common.ResourceScopeContainer} {
		status, response := updatePerformanceOption(t, "performance_setting.monitor_resource_scope", scope)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, true, response["success"])
		assert.Equal(t, scope, common.GetPerformanceMonitorConfig().ResourceScope)
	}

	var option model.Option
	require.NoError(t, db.First(&option, "key = ?", "performance_setting.monitor_resource_scope").Error)
	assert.Equal(t, common.ResourceScopeContainer, option.Value)
}

func TestUpdateOptionRejectsInvalidPerformanceSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupPerformanceOptionTest(t)
	tests := []struct {
		name  string
		key   string
		value any
	}{
		{name: "negative retention", key: "performance_setting.server_log_retention_days", value: -1},
		{name: "retention too large", key: "performance_setting.server_log_retention_days", value: 3651},
		{name: "non integer retention", key: "performance_setting.server_log_retention_days", value: 7.5},
		{name: "invalid scope", key: "performance_setting.monitor_resource_scope", value: "process"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, response := updatePerformanceOption(t, test.key, test.value)
			assert.Equal(t, http.StatusOK, status)
			assert.Equal(t, false, response["success"])
			assert.NotEmpty(t, response["message"])
		})
	}
}

func TestPerformanceStatsKeepsExistingFieldsAndAddsResourceStats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetPerformanceStats(ctx)

	var response map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, true, response["success"])
	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok)
	for _, key := range []string{"cache_stats", "memory_stats", "disk_cache_info", "disk_space_info", "config", "resource_stats"} {
		assert.Contains(t, data, key)
	}
	resourceStats, ok := data["resource_stats"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, resourceStats, "configured_scope")
	assert.Contains(t, resourceStats, "effective_cpu_scope")
	assert.Contains(t, resourceStats, "effective_memory_scope")
	assert.Contains(t, resourceStats, "gomaxprocs")
}

func TestGetLogFilesIncludesAutomaticRetentionState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "oneapi-20260818000000.log"), []byte("fixture"), 0o600))
	previousDir := *common.LogDir
	previousDays := common.GetServerLogRetentionDays()
	*common.LogDir = dir
	common.SetServerLogRetentionDays(7)
	t.Cleanup(func() {
		*common.LogDir = previousDir
		common.SetServerLogRetentionDays(previousDays)
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	GetLogFiles(ctx)
	var response map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, data["enabled"])
	assert.Equal(t, true, data["auto_cleanup_enabled"])
	assert.EqualValues(t, 7, data["retention_days"])
	assert.EqualValues(t, 1, data["file_count"])
}
