package operation_setting

import (
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/setting/config"
)

const (
	ChannelAutoDisableMinWindowMinutes  = 1
	ChannelAutoDisableMaxWindowMinutes  = 60
	ChannelAutoDisableMinRequests       = 1
	ChannelAutoDisableMaxRequests       = 100000
	ChannelAutoDisableMinErrorRate      = 1
	ChannelAutoDisableMaxErrorRate      = 100
	ChannelAutoDisableMinDisableMinutes = 1
	ChannelAutoDisableMaxDisableMinutes = 1440
)

// ChannelAutoDisableSetting controls temporary channel disabling based on upstream HTTP error rates.
type ChannelAutoDisableSetting struct {
	StatusCodes      string `json:"status_codes"`
	WindowMinutes    int    `json:"window_minutes"`
	MinRequests      int    `json:"min_requests"`
	ErrorRatePercent int    `json:"error_rate_percent"`
	DisableMinutes   int    `json:"disable_minutes"`
}

var defaultChannelAutoDisableSetting = ChannelAutoDisableSetting{
	StatusCodes:      "400-599",
	WindowMinutes:    10,
	MinRequests:      30,
	ErrorRatePercent: 80,
	DisableMinutes:   10,
}

var channelAutoDisableSetting = defaultChannelAutoDisableSetting

func init() {
	config.GlobalConfig.Register("channel_auto_disable_setting", &channelAutoDisableSetting)
}

// GetChannelAutoDisableSetting returns the current global temporary auto-disable settings.
func GetChannelAutoDisableSetting() ChannelAutoDisableSetting {
	setting := channelAutoDisableSetting
	if ranges, err := ParseHTTPStatusCodeRanges(setting.StatusCodes); err != nil || len(ranges) == 0 {
		setting.StatusCodes = defaultChannelAutoDisableSetting.StatusCodes
	} else {
		setting.StatusCodes = statusCodeRangesToString(ranges)
	}
	if setting.WindowMinutes < ChannelAutoDisableMinWindowMinutes || setting.WindowMinutes > ChannelAutoDisableMaxWindowMinutes {
		setting.WindowMinutes = defaultChannelAutoDisableSetting.WindowMinutes
	}
	if setting.MinRequests < ChannelAutoDisableMinRequests || setting.MinRequests > ChannelAutoDisableMaxRequests {
		setting.MinRequests = defaultChannelAutoDisableSetting.MinRequests
	}
	if setting.ErrorRatePercent < ChannelAutoDisableMinErrorRate || setting.ErrorRatePercent > ChannelAutoDisableMaxErrorRate {
		setting.ErrorRatePercent = defaultChannelAutoDisableSetting.ErrorRatePercent
	}
	if setting.DisableMinutes < ChannelAutoDisableMinDisableMinutes || setting.DisableMinutes > ChannelAutoDisableMaxDisableMinutes {
		setting.DisableMinutes = defaultChannelAutoDisableSetting.DisableMinutes
	}
	return setting
}

// ShouldCountChannelAutoDisableStatusCode reports whether an upstream status belongs to the configured error ranges.
func ShouldCountChannelAutoDisableStatusCode(code int) bool {
	setting := GetChannelAutoDisableSetting()
	ranges, err := ParseHTTPStatusCodeRanges(setting.StatusCodes)
	if err != nil {
		return false
	}
	return shouldMatchStatusCodeRanges(ranges, code)
}

// NormalizeChannelAutoDisableOption validates and normalizes one persisted option value.
func NormalizeChannelAutoDisableOption(key string, value string) (string, error) {
	switch key {
	case "channel_auto_disable_setting.status_codes":
		ranges, err := ParseHTTPStatusCodeRanges(value)
		if err != nil {
			return "", err
		}
		if len(ranges) == 0 {
			return "", fmt.Errorf("statistical auto-disable status codes cannot be empty")
		}
		return statusCodeRangesToString(ranges), nil
	case "channel_auto_disable_setting.window_minutes":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinWindowMinutes, ChannelAutoDisableMaxWindowMinutes, "window minutes")
	case "channel_auto_disable_setting.min_requests":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinRequests, ChannelAutoDisableMaxRequests, "minimum requests")
	case "channel_auto_disable_setting.error_rate_percent":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinErrorRate, ChannelAutoDisableMaxErrorRate, "error rate percent")
	case "channel_auto_disable_setting.disable_minutes":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinDisableMinutes, ChannelAutoDisableMaxDisableMinutes, "disable minutes")
	default:
		return value, nil
	}
}

// normalizeChannelAutoDisableInt validates one bounded integer option.
func normalizeChannelAutoDisableInt(value string, minValue int, maxValue int, label string) (string, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minValue || parsed > maxValue {
		return "", fmt.Errorf("%s must be an integer between %d and %d", label, minValue, maxValue)
	}
	return strconv.Itoa(parsed), nil
}
