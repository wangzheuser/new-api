package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogListFinalResultVisibility verifies the admin default and the user-enforced final-result contract.
func TestLogListFinalResultVisibility(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Create([]*model.Log{
		{UserId: 1, CreatedAt: 100, Type: model.LogTypeError, Content: "retry upstream 503", RequestId: "req-success"},
		{UserId: 1, CreatedAt: 101, Type: model.LogTypeConsume, Content: "success", RequestId: "req-success"},
		{UserId: 1, CreatedAt: 102, Type: model.LogTypeError, Content: "retry upstream 503", RequestId: "req-failure"},
		{UserId: 1, CreatedAt: 103, Type: model.LogTypeError, Content: "当前分组上游负载已饱和", RequestId: "req-failure", Other: `{"public_error":true}`},
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
			wantContents: []string{"retry upstream 503", "success", "retry upstream 503", "当前分组上游负载已饱和"},
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
