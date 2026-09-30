package operation_setting

import (
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/setting/config"
)

const (
	ChannelAutoDisableMinSampleSize     = 1
	ChannelAutoDisableMaxSampleSize     = 1000
	ChannelAutoDisableMinMinimumSamples = 1
	ChannelAutoDisableMinErrorRate      = 1
	ChannelAutoDisableMaxErrorRate      = 100
	ChannelAutoDisableMinDisableMinutes = 1
	ChannelAutoDisableMaxDisableMinutes = 1440
)

// ChannelAutoDisableSetting controls temporary channel disabling based on upstream HTTP error rates.
type ChannelAutoDisableSetting struct {
	StatusCodes       string `json:"status_codes"`
	SampleSize        int    `json:"sample_size"`
	MinimumSampleSize int    `json:"minimum_sample_size"`
	ErrorRatePercent  int    `json:"error_rate_percent"`
	DisableMinutes    int    `json:"disable_minutes"`
}

var defaultChannelAutoDisableSetting = ChannelAutoDisableSetting{
	StatusCodes:       "408,500-599",
	SampleSize:        20,
	MinimumSampleSize: 3,
	ErrorRatePercent:  80,
	DisableMinutes:    10,
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
	if setting.SampleSize < ChannelAutoDisableMinSampleSize || setting.SampleSize > ChannelAutoDisableMaxSampleSize {
		setting.SampleSize = defaultChannelAutoDisableSetting.SampleSize
	}
	if setting.MinimumSampleSize < ChannelAutoDisableMinMinimumSamples || setting.MinimumSampleSize > setting.SampleSize {
		setting.MinimumSampleSize = defaultChannelAutoDisableSetting.MinimumSampleSize
		if setting.MinimumSampleSize > setting.SampleSize {
			setting.MinimumSampleSize = setting.SampleSize
		}
	}
	if setting.ErrorRatePercent < ChannelAutoDisableMinErrorRate || setting.ErrorRatePercent > ChannelAutoDisableMaxErrorRate {
		setting.ErrorRatePercent = defaultChannelAutoDisableSetting.ErrorRatePercent
	}
	if setting.DisableMinutes < ChannelAutoDisableMinDisableMinutes || setting.DisableMinutes > ChannelAutoDisableMaxDisableMinutes {
		setting.DisableMinutes = defaultChannelAutoDisableSetting.DisableMinutes
	}
	return setting
}

// MigrateChannelAutoDisableOptions 在加载边界迁移旧字段；显式新字段始终优先。
// 返回副本，避免批量更新时修改调用方的配置集合。
func MigrateChannelAutoDisableOptions(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	const legacyKey = "channel_auto_disable_setting.min_requests"
	const minimumKey = "channel_auto_disable_setting.minimum_sample_size"
	legacy, exists := values[legacyKey]
	delete(result, legacyKey)
	if _, explicit := values[minimumKey]; explicit || !exists {
		return result
	}
	minimum, err := strconv.Atoi(legacy)
	if err != nil || minimum < 1 || minimum > ChannelAutoDisableMaxSampleSize {
		return result
	}
	sampleSize := defaultChannelAutoDisableSetting.SampleSize
	if value, err := strconv.Atoi(values["channel_auto_disable_setting.sample_size"]); err == nil && value >= 1 && value <= ChannelAutoDisableMaxSampleSize {
		sampleSize = value
	}
	result[minimumKey] = strconv.Itoa(min(minimum, sampleSize))
	return result
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
	case "channel_auto_disable_setting.sample_size":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinSampleSize, ChannelAutoDisableMaxSampleSize, "sample size")
	case "channel_auto_disable_setting.minimum_sample_size":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinMinimumSamples, ChannelAutoDisableMaxSampleSize, "minimum sample size")
	case "channel_auto_disable_setting.min_requests":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinMinimumSamples, ChannelAutoDisableMaxSampleSize, "minimum sample size")
	case "channel_auto_disable_setting.error_rate_percent":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinErrorRate, ChannelAutoDisableMaxErrorRate, "error rate percent")
	case "channel_auto_disable_setting.disable_minutes":
		return normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinDisableMinutes, ChannelAutoDisableMaxDisableMinutes, "disable minutes")
	default:
		return value, nil
	}
}

// ValidateChannelAutoDisableOption checks one update against the current pair of sample thresholds.
func ValidateChannelAutoDisableOption(key, value string) error {
	if key != "channel_auto_disable_setting.sample_size" && key != "channel_auto_disable_setting.minimum_sample_size" && key != "channel_auto_disable_setting.min_requests" {
		return nil
	}
	candidate, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	label := "sample size"
	if key == "channel_auto_disable_setting.minimum_sample_size" || key == "channel_auto_disable_setting.min_requests" {
		label = "minimum sample size"
	}
	if _, err := normalizeChannelAutoDisableInt(value, ChannelAutoDisableMinSampleSize, ChannelAutoDisableMaxSampleSize, label); err != nil {
		return err
	}
	setting := GetChannelAutoDisableSetting()
	sampleSize, minimumSampleSize := setting.SampleSize, setting.MinimumSampleSize
	if key == "channel_auto_disable_setting.sample_size" {
		sampleSize = candidate
	} else {
		minimumSampleSize = candidate
	}
	if minimumSampleSize > sampleSize {
		return fmt.Errorf("minimum sample size cannot exceed sample size")
	}
	return nil
}

// normalizeChannelAutoDisableInt validates one bounded integer option.
func normalizeChannelAutoDisableInt(value string, minValue int, maxValue int, label string) (string, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minValue || parsed > maxValue {
		return "", fmt.Errorf("%s must be an integer between %d and %d", label, minValue, maxValue)
	}
	return strconv.Itoa(parsed), nil
}
