package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogStatsExposeTokenBreakdownOnlyToAdmin verifies the aggregate contract and self-query isolation.
func TestLogStatsExposeTokenBreakdownOnlyToAdmin(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	now := time.Now().Unix()
	require.NoError(t, db.Create([]*model.Log{
		{
			UserId: 1, Username: "alice", CreatedAt: now, Type: model.LogTypeConsume,
			Quota: 100, PromptTokens: 170, CompletionTokens: 20, InputTokens: 100,
			CacheCreationTokens: 30, CacheReadTokens: 40, RequestId: "req-new",
		},
		{
			UserId: 1, Username: "alice", CreatedAt: now, Type: model.LogTypeConsume,
			Quota: 50, PromptTokens: 50, CompletionTokens: 5, RequestId: "req-legacy",
		},
	}).Error)

	type logStatsResponse struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}

	adminRecorder := httptest.NewRecorder()
	adminContext, _ := gin.CreateTestContext(adminRecorder)
	adminContext.Request = httptest.NewRequest(http.MethodGet, "/api/log/stat?start_timestamp="+strconv.FormatInt(now-1, 10), nil)
	GetLogsStat(adminContext)

	var adminResponse logStatsResponse
	require.NoError(t, common.Unmarshal(adminRecorder.Body.Bytes(), &adminResponse))
	require.True(t, adminResponse.Success)
	assert.EqualValues(t, 2, adminResponse.Data["request_count"])
	assert.EqualValues(t, 150, adminResponse.Data["input_tokens"])
	assert.EqualValues(t, 25, adminResponse.Data["output_tokens"])
	assert.EqualValues(t, 30, adminResponse.Data["cache_creation_tokens"])
	assert.EqualValues(t, 40, adminResponse.Data["cache_read_tokens"])

	selfRecorder := httptest.NewRecorder()
	selfContext, _ := gin.CreateTestContext(selfRecorder)
	selfContext.Request = httptest.NewRequest(http.MethodGet, "/api/log/self/stat?start_timestamp="+strconv.FormatInt(now-1, 10), nil)
	selfContext.Set("username", "alice")
	GetLogsSelfStat(selfContext)

	var selfResponse logStatsResponse
	require.NoError(t, common.Unmarshal(selfRecorder.Body.Bytes(), &selfResponse))
	require.True(t, selfResponse.Success)
	assert.NotContains(t, selfResponse.Data, "request_count")
	assert.NotContains(t, selfResponse.Data, "input_tokens")
	assert.NotContains(t, selfResponse.Data, "output_tokens")
	assert.NotContains(t, selfResponse.Data, "cache_creation_tokens")
	assert.NotContains(t, selfResponse.Data, "cache_read_tokens")
}

// TestLogListFinalResultVisibility verifies the admin default and the user-enforced final-result contract.
func TestLogListFinalResultVisibility(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Create([]*model.Log{
		{UserId: 1, CreatedAt: 100, Type: model.LogTypeError, Content: "retry upstream 503", RequestId: "req-success", IsIntermediate: true},
		{UserId: 1, CreatedAt: 101, Type: model.LogTypeConsume, Content: "success", RequestId: "req-success"},
		{UserId: 1, CreatedAt: 102, Type: model.LogTypeError, Content: "retry upstream 503", RequestId: "req-failure", IsIntermediate: true},
		{UserId: 1, CreatedAt: 103, Type: model.LogTypeError, Content: "当前分组上游负载已饱和", RequestId: "req-failure", Other: `{"public_error":true}`},
		{UserId: 1, CreatedAt: 104, Type: model.LogTypeError, Content: "in-flight upstream 503", RequestId: "req-in-flight", IsIntermediate: true},
	}).Error)

	type logListResponse struct {
		Success bool `json:"success"`
		Data    struct {
			Total int          `json:"total"`
			Items []*model.Log `json:"items"`
		} `json:"data"`
	}

	tests := []struct {
		name         string
		url          string
		handler      gin.HandlerFunc
		wantContents []string
	}{
		{
			name:         "admin defaults to final results",
			url:          "/api/log/?p=1&page_size=20",
			handler:      GetAllLogs,
			wantContents: []string{"success", "当前分组上游负载已饱和"},
		},
		{
			name:         "admin can request every retry attempt",
			url:          "/api/log/?p=1&page_size=20&latest_per_request=false",
			handler:      GetAllLogs,
			wantContents: []string{"retry upstream 503", "success", "retry upstream 503", "当前分组上游负载已饱和", "in-flight upstream 503"},
		},
		{
			name:         "user cannot disable final results",
			url:          "/api/log/self?p=1&page_size=20&latest_per_request=false",
			handler:      GetUserLogs,
			wantContents: []string{"success", "当前分组上游负载已饱和"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, test.url, nil)
			ctx.Set("id", 1)

			test.handler(ctx)

			var response logListResponse
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.True(t, response.Success)
			assert.Equal(t, len(test.wantContents), response.Data.Total)

			contents := make([]string, 0, len(response.Data.Items))
			for _, log := range response.Data.Items {
				contents = append(contents, log.Content)
			}
			assert.ElementsMatch(t, test.wantContents, contents)
		})
	}
}

// TestSanitizeHistoricalUserRelayLogs verifies old raw errors use the configured system fallback.
func TestSanitizeHistoricalUserRelayLogs(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previous := settings.DefaultFinalErrorOverride
	t.Cleanup(func() {
		settings.DefaultFinalErrorOverride = previous
	})
	settings.DefaultFinalErrorOverride = finalErrorOverrideForTest("公共错误", 503, "service_unavailable")

	logs := []*model.Log{
		{
			Type:    model.LogTypeError,
			Content: "status_code=503, raw upstream vendor error",
			Other:   `{"status_code":503,"error_code":"upstream_error"}`,
		},
		{
			Type:    model.LogTypeError,
			Content: "status_code=400, 已清洗错误",
			Other:   `{"status_code":400,"public_error":true}`,
		},
	}

	sanitizeHistoricalUserRelayLogs(logs)

	assert.Equal(t, "status_code=503, 公共错误", logs[0].Content)
	assert.Equal(t, "status_code=400, 已清洗错误", logs[1].Content)
}
