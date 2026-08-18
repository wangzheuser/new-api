package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	ModelRequestRateLimitCountMark        = "MRRL"
	ModelRequestRateLimitSuccessCountMark = "MRRLS"
)

type modelRequestRateLimitPolicy struct {
	totalMaxCount   int
	successMaxCount int
	userRule        *model.UserModelRateLimit
}

// checkRedisRateLimit checks the existing sliding success-request window.
func checkRedisRateLimit(ctx context.Context, rdb *redis.Client, key string, maxCount int, duration int64) (bool, error) {
	if maxCount == 0 {
		return true, nil
	}

	length, err := rdb.LLen(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if length < int64(maxCount) {
		return true, nil
	}

	oldTimeStr, _ := rdb.LIndex(ctx, key, -1).Result()
	oldTime, err := time.Parse(timeFormat, oldTimeStr)
	if err != nil {
		return false, err
	}
	nowTime, err := time.Parse(timeFormat, time.Now().Format(timeFormat))
	if err != nil {
		return false, err
	}
	if int64(nowTime.Sub(oldTime).Seconds()) < duration {
		rdb.Expire(ctx, key, time.Duration(setting.ModelRequestRateLimitDurationMinutes)*time.Minute)
		return false, nil
	}
	return true, nil
}

// recordRedisRequest records a successful request in the existing sliding window.
func recordRedisRequest(ctx context.Context, rdb *redis.Client, key string, maxCount int) {
	if maxCount == 0 {
		return
	}

	rdb.LPush(ctx, key, time.Now().Format(timeFormat))
	rdb.LTrim(ctx, key, 0, int64(maxCount-1))
	rdb.Expire(ctx, key, time.Duration(setting.ModelRequestRateLimitDurationMinutes)*time.Minute)
}

// modelRequestRateLimitKey builds one user-and-group rate-limit key.
func modelRequestRateLimitKey(mark, subject string) string {
	return fmt.Sprintf("rateLimit:%s:%s", mark, subject)
}

// modelRequestRateLimitGroup returns the group bucket visible at the middleware's current route position.
func modelRequestRateLimitGroup(c *gin.Context) string {
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "" {
		group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	return group
}

// modelRequestRateLimitSubject returns the current user-and-group bucket subject.
func modelRequestRateLimitSubject(c *gin.Context) string {
	return strconv.Itoa(c.GetInt("id")) + ":" + modelRequestRateLimitGroup(c)
}

// redisRateLimitHandler enforces total requests before the post-success sliding window.
func redisRateLimitHandler(duration int64, policy modelRequestRateLimitPolicy, requestStart time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject := modelRequestRateLimitSubject(c)
		group := modelRequestRateLimitGroup(c)
		ctx := c.Request.Context()
		rdb := common.RDB

		if policy.totalMaxCount > 0 {
			totalKey := modelRequestRateLimitKey(ModelRequestRateLimitCountMark, subject)
			tokenBucket := limiter.New(ctx, rdb)
			allowed, err := tokenBucket.Allow(
				ctx,
				totalKey,
				limiter.WithCapacity(int64(policy.totalMaxCount)*duration),
				limiter.WithRate(int64(policy.totalMaxCount)),
				limiter.WithRequested(duration),
			)
			if err != nil {
				logger.LogError(ctx, "failed to check total model request rate limit: "+err.Error())
				abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
				return
			}
			if !allowed {
				message := fmt.Sprintf("您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确", setting.ModelRequestRateLimitDurationMinutes, policy.totalMaxCount)
				rejectModelRequestRateLimit(c, policy, group, "total", message, requestStart, true)
				return
			}
		}

		successKey := modelRequestRateLimitKey(ModelRequestRateLimitSuccessCountMark, subject)
		allowed, err := checkRedisRateLimit(ctx, rdb, successKey, policy.successMaxCount, duration)
		if err != nil {
			logger.LogError(ctx, "failed to check successful model request rate limit: "+err.Error())
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
			return
		}
		if !allowed {
			message := fmt.Sprintf("您已达到请求数限制：%d分钟内最多请求%d次", setting.ModelRequestRateLimitDurationMinutes, policy.successMaxCount)
			rejectModelRequestRateLimit(c, policy, group, "success", message, requestStart, true)
			return
		}

		c.Next()
		if c.Writer.Status() < http.StatusBadRequest {
			recordRedisRequest(ctx, rdb, successKey, policy.successMaxCount)
		}
	}
}

// memoryRateLimitHandler mirrors the Redis total-first rule selection using the existing in-memory counters.
func memoryRateLimitHandler(duration int64, policy modelRequestRateLimitPolicy, requestStart time.Time) gin.HandlerFunc {
	inMemoryRateLimiter.Init(time.Duration(setting.ModelRequestRateLimitDurationMinutes) * time.Minute)

	return func(c *gin.Context) {
		subject := modelRequestRateLimitSubject(c)
		group := modelRequestRateLimitGroup(c)
		totalKey := modelRequestRateLimitKey(ModelRequestRateLimitCountMark, subject)
		successKey := modelRequestRateLimitKey(ModelRequestRateLimitSuccessCountMark, subject)

		if policy.totalMaxCount > 0 && !inMemoryRateLimiter.Request(totalKey, policy.totalMaxCount, duration) {
			message := fmt.Sprintf("您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确", setting.ModelRequestRateLimitDurationMinutes, policy.totalMaxCount)
			rejectModelRequestRateLimit(c, policy, group, "total", message, requestStart, false)
			return
		}
		if !inMemoryRateLimiter.Available(successKey, policy.successMaxCount, duration) {
			message := fmt.Sprintf("您已达到请求数限制：%d分钟内最多请求%d次", setting.ModelRequestRateLimitDurationMinutes, policy.successMaxCount)
			rejectModelRequestRateLimit(c, policy, group, "success", message, requestStart, false)
			return
		}

		c.Next()
		if c.Writer.Status() < http.StatusBadRequest {
			inMemoryRateLimiter.Request(successKey, policy.successMaxCount, duration)
		}
	}
}

// rejectModelRequestRateLimit preserves legacy responses unless a user-group rule selected the custom policy.
func rejectModelRequestRateLimit(c *gin.Context, policy modelRequestRateLimitPolicy, group, kind, legacyMessage string, requestStart time.Time, redisBackend bool) {
	if policy.userRule == nil {
		if redisBackend {
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, legacyMessage)
		} else {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
		}
		return
	}

	response, responseSource, delaySeconds := setting.ResolveUserModelRateLimitResponse(
		group,
		policy.userRule.StatusCode,
		policy.userRule.ErrorMessage,
	)
	if delaySeconds > 0 {
		timer := time.NewTimer(time.Duration(delaySeconds) * time.Second)
		select {
		case <-timer.C:
		case <-c.Request.Context().Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			logCanceledUserModelRateLimit(c, group, policy.userRule.Id)
			c.Abort()
			return
		}
	}
	select {
	case <-c.Request.Context().Done():
		logCanceledUserModelRateLimit(c, group, policy.userRule.Id)
		c.Abort()
		return
	default:
	}

	finalMessage := common.MessageWithRequestId(response.ErrorMessage, c.GetString(common.RequestIdKey))
	c.AbortWithStatusJSON(response.StatusCode, gin.H{
		"error": gin.H{
			"message": finalMessage,
			"type":    "new_api_error",
			"code":    "model_rate_limit_exceeded",
		},
	})
	c.Writer.Flush()

	if !constant.ErrorLogEnabled {
		return
	}
	other := map[string]interface{}{
		"status_code":                response.StatusCode,
		"error_type":                 "new_api_error",
		"error_code":                 "model_rate_limit_exceeded",
		"public_error":               true,
		"request_path":               c.Request.URL.Path,
		"rate_limit_scope":           "user_group",
		"rate_limit_kind":            kind,
		"rate_limit_rule_id":         policy.userRule.Id,
		"rate_limit_response_source": responseSource,
		"delay_seconds":              delaySeconds,
		"response_written":           true,
	}
	model.RecordErrorLog(
		c,
		c.GetInt("id"),
		0,
		"",
		c.GetString("token_name"),
		fmt.Sprintf("status_code=%d, %s", response.StatusCode, finalMessage),
		c.GetInt("token_id"),
		int(time.Since(requestStart).Seconds()),
		false,
		false,
		group,
		other,
	)
}

// logCanceledUserModelRateLimit records cancellation metadata without copying the configured client message.
func logCanceledUserModelRateLimit(c *gin.Context, group string, ruleId int) {
	logger.LogInfo(c.Request.Context(), fmt.Sprintf(
		"user model rate-limit response canceled: request_id=%s user_id=%d group=%s rule_id=%d",
		c.GetString(common.RequestIdKey), c.GetInt("id"), group, ruleId,
	))
}

// ModelRequestRateLimit resolves the global, group, then user-group count hierarchy for each request.
func ModelRequestRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestStart := time.Now()
		if !setting.ModelRequestRateLimitEnabled {
			c.Next()
			return
		}

		policy := modelRequestRateLimitPolicy{
			totalMaxCount:   setting.ModelRequestRateLimitCount,
			successMaxCount: setting.ModelRequestRateLimitSuccessCount,
		}
		group := modelRequestRateLimitGroup(c)
		if groupTotalCount, groupSuccessCount, found := setting.GetGroupRateLimit(group); found {
			policy.totalMaxCount = groupTotalCount
			policy.successMaxCount = groupSuccessCount
		}

		rules, err := model.GetUserModelRateLimits(c.GetInt("id"))
		if err != nil {
			logger.LogError(c.Request.Context(), "failed to load user model rate-limit rules: "+err.Error())
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
			return
		}
		if rule, found := rules[group]; found {
			policy.totalMaxCount = rule.TotalCount
			policy.successMaxCount = rule.SuccessCount
			policy.userRule = &rule
		}

		duration := int64(setting.ModelRequestRateLimitDurationMinutes * 60)
		if common.RedisEnabled {
			redisRateLimitHandler(duration, policy, requestStart)(c)
		} else {
			memoryRateLimitHandler(duration, policy, requestStart)(c)
		}
	}
}
