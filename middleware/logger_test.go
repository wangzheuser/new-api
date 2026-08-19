package middleware

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestFormatAccessLogSuppressesOnlySuccessfulStatusChecks(t *testing.T) {
	base := gin.LogFormatterParams{
		TimeStamp: time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC),
		Latency:   15 * time.Millisecond,
		ClientIP:  "127.0.0.1",
		Method:    http.MethodGet,
		Keys: map[string]any{
			common.RequestIdKey: "REQUEST_ID",
			RouteTagKey:         "api",
		},
	}

	successStatus := base
	successStatus.Path = "/api/status"
	successStatus.StatusCode = http.StatusOK
	assert.Empty(t, formatAccessLog(successStatus))

	failedStatus := base
	failedStatus.Path = "/api/status"
	failedStatus.StatusCode = http.StatusInternalServerError
	failedLine := formatAccessLog(failedStatus)
	assert.Contains(t, failedLine, "REQUEST_ID")
	assert.Contains(t, failedLine, "500")
	assert.Contains(t, failedLine, "/api/status")
	assert.True(t, strings.HasSuffix(failedLine, "\n"))

	for _, path := range []string{"/api/performance/stats", "/v1/chat/completions"} {
		request := base
		request.Path = path
		request.StatusCode = http.StatusOK
		assert.Contains(t, formatAccessLog(request), path)
	}
}
