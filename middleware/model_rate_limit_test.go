package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupModelRateLimitTest configures deterministic in-memory rate limiting.
func setupModelRateLimitTest(t *testing.T, groupLimits string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousEnabled := setting.ModelRequestRateLimitEnabled
	previousDuration := setting.ModelRequestRateLimitDurationMinutes
	previousTotal := setting.ModelRequestRateLimitCount
	previousSuccess := setting.ModelRequestRateLimitSuccessCount
	previousGroups := setting.ModelRequestRateLimitGroup2JSONString()
	previousRedisEnabled := common.RedisEnabled

	setting.ModelRequestRateLimitEnabled = true
	setting.ModelRequestRateLimitDurationMinutes = 1
	setting.ModelRequestRateLimitCount = 0
	setting.ModelRequestRateLimitSuccessCount = 100
	require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(groupLimits))
	common.RedisEnabled = false
	inMemoryRateLimiter = common.InMemoryRateLimiter{}

	t.Cleanup(func() {
		setting.ModelRequestRateLimitEnabled = previousEnabled
		setting.ModelRequestRateLimitDurationMinutes = previousDuration
		setting.ModelRequestRateLimitCount = previousTotal
		setting.ModelRequestRateLimitSuccessCount = previousSuccess
		require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(previousGroups))
		common.RedisEnabled = previousRedisEnabled
		inMemoryRateLimiter = common.InMemoryRateLimiter{}
	})

	router := gin.New()
	router.GET("/:user/:group", func(c *gin.Context) {
		var userId int
		_, err := fmt.Sscanf(c.Param("user"), "%d", &userId)
		require.NoError(t, err)
		c.Set("id", userId)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, c.Param("group"))
		common.SetContextKey(c, constant.ContextKeyTokenGroup, "raw-token-group")
		c.Next()
	}, ModelRequestRateLimit(), func(c *gin.Context) {
		if c.Query("fail") == "1" {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})
	return router
}

// TestModelRequestRateLimitSeparatesUserGroups verifies buckets are user-and-group scoped.
func TestModelRequestRateLimitSeparatesUserGroups(t *testing.T) {
	router := setupModelRateLimitTest(t, `{"group-a":[1,10],"group-b":[1,10],"raw-token-group":[0,1]}`)

	request := func(path string) int {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Code
	}

	assert.Equal(t, http.StatusOK, request("/101/group-a"))
	assert.Equal(t, http.StatusTooManyRequests, request("/101/group-a"))
	assert.Equal(t, http.StatusOK, request("/101/group-b"))
	assert.Equal(t, http.StatusOK, request("/202/group-a"))
}

// TestMemoryModelRequestRateLimitCountsOnlySuccessfulResponses verifies failures do not consume success capacity.
func TestMemoryModelRequestRateLimitCountsOnlySuccessfulResponses(t *testing.T) {
	router := setupModelRateLimitTest(t, `{"success-only":[0,1]}`)

	request := func(path string) int {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		return recorder.Code
	}

	assert.Equal(t, http.StatusInternalServerError, request("/303/success-only?fail=1"))
	assert.Equal(t, http.StatusOK, request("/303/success-only"))
	assert.Equal(t, http.StatusTooManyRequests, request("/303/success-only"))
}

// TestModelRequestRateLimitKeyIncludesGroup locks the Redis and memory key contract.
func TestModelRequestRateLimitKeyIncludesGroup(t *testing.T) {
	assert.Equal(t, "rateLimit:MRRL:101:group-a", modelRequestRateLimitKey(ModelRequestRateLimitCountMark, "101:group-a"))
	assert.Equal(t, "rateLimit:MRRLS:101:group-a", modelRequestRateLimitKey(ModelRequestRateLimitSuccessCountMark, "101:group-a"))
}
