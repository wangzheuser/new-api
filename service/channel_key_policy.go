package service

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

// MultiKeyDecision describes one upstream failure without changing public HTTP errors.
type MultiKeyDecision struct {
	Action                         MultiKeyFailureAction
	StatusCode                     int
	Scope, Model, Category, Source string
	RecoverAt                      int64
}

var dailyModelLimit = regexp.MustCompile(`(?i)daily free limit reached on model ([^ .]+(?:\.[^ .]+)*)\. try again in ([0-9]+)h ([0-9]+)m`)

// DecideMultiKeyFailure separates credential failures from provider quota scopes.
func DecideMultiKeyFailure(channel *model.Channel, upstreamModel string, apiErr *types.NewAPIError, now time.Time) MultiKeyDecision {
	d := MultiKeyDecision{Action: MultiKeyFailureNone, Scope: "key", Source: "global"}
	if channel == nil || apiErr == nil || !channel.ChannelInfo.IsMultiKey || !common.AutomaticDisableChannelEnabled || !channel.GetAutoBan() {
		return d
	}
	status, real := apiErr.GetUpstreamStatusCode()
	if !real {
		return d
	}
	d.StatusCode = status
	if channel.GetOtherSettings().MultiKeyAutoDisableOverride != nil {
		d.Source = "channel"
	}
	setting := effectiveMultiKeyAutoDisableConfig(channel)
	message := strings.ToLower(apiErr.Error())
	if isIgnoredChannelHealthError(message) {
		return d
	}
	providerCode := fmt.Sprint(apiErr.ToOpenAIError().Code)
	// Credential failure is always persistent; quota and rate-limit failures are always temporary.
	if status == http.StatusUnauthorized {
		d.Action, d.Category = MultiKeyFailurePersistent, "credential"
		return d
	}
	quota := false
	if match := dailyModelLimit.FindStringSubmatch(message); len(match) == 4 && upstreamModel != "" {
		d.Scope, d.Model, d.Category = "model", upstreamModel, "daily_quota"
		hours, e1 := strconv.ParseInt(match[2], 10, 32)
		minutes, e2 := strconv.ParseInt(match[3], 10, 32)
		if e1 == nil && e2 == nil && minutes < 60 && hours < 168 {
			d.RecoverAt = now.Add(time.Duration(hours*60+minutes) * time.Minute).Unix()
		}
		quota = true
	} else if providerCode == "INFERENCE_CAP_ERROR" && upstreamModel != "" {
		d.Scope, d.Model, d.Category, quota = "model", upstreamModel, "model_quota", true
	} else if isProviderQuotaMessage(message) {
		d.Scope, d.Model, d.Category, quota = "model", upstreamModel, "provider_quota", true
		if d.Model == "" {
			d.Model = "unknown"
		}
	} else if isAccountQuotaMessage(message) || providerCode == "insufficient_quota" {
		d.Scope, d.Model, d.Category, quota = "model", upstreamModel, "account_quota", true
		if d.Model == "" {
			d.Model = "unknown"
		}
	} else if status == http.StatusTooManyRequests {
		d.Scope, d.Model, d.Category, quota = "model", upstreamModel, "rate_limit", true
	}
	if quota {
		if d.Model == "" {
			d.Model = "unknown"
		}
		d.Source += ":provider"
		d.Action = MultiKeyFailureTemporary
		if header := apiErr.GetUpstreamRetryAfter(); header != "" {
			at, valid := parseMultiKeyRetryAfter(header, now)
			if valid && at > d.RecoverAt {
				d.RecoverAt = at
			}
			if !valid {
				common.SysLog(fmt.Sprintf("invalid multi-key recovery hint: channel_id=%d", channel.Id))
			}
		}
		return d
	}
	if operation_setting.MatchMultiKeyStatusCode(setting.PersistentStatusCodes, status) {
		d.Action, d.Category = MultiKeyFailurePersistent, "credential"
		return d
	}
	// 408 and 5xx are statistical health failures; they must not be converted into key cooldowns.
	if !quota && (status == http.StatusRequestTimeout || status >= 500) {
		return d
	}
	if !quota && !operation_setting.MatchMultiKeyStatusCode(setting.TemporaryStatusCodes, status) {
		return d
	}
	d.Action = MultiKeyFailureTemporary
	d.Category = "rate_limit"
	if header := apiErr.GetUpstreamRetryAfter(); header != "" {
		at, valid := parseMultiKeyRetryAfter(header, now)
		if valid && at > d.RecoverAt {
			d.RecoverAt = at
		}
		if !valid {
			common.SysLog(fmt.Sprintf("invalid multi-key recovery hint: channel_id=%d", channel.Id))
		}
	}
	return d
}

// parseMultiKeyRetryAfter accepts bounded future HTTP dates and integer delays.
func parseMultiKeyRetryAfter(value string, now time.Time) (int64, bool) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 || seconds > 7*24*3600 {
			return 0, false
		}
		return now.Unix() + seconds, true
	}
	at, err := http.ParseTime(value)
	return at.Unix(), err == nil && at.After(now) && !at.After(now.Add(7*24*time.Hour))
}
