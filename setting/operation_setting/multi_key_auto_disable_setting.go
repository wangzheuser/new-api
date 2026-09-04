package operation_setting

import (
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/setting/config"
)

const (
	MultiKeyTemporaryDisableMinMinutes = 1
	MultiKeyTemporaryDisableMaxMinutes = 1440
)

// MultiKeyAutoDisableSetting controls per-key temporary and persistent disable decisions.
type MultiKeyAutoDisableSetting struct {
	TemporaryStatusCodes    string `json:"temporary_status_codes"`
	PersistentStatusCodes   string `json:"persistent_status_codes"`
	TemporaryDisableMinutes int    `json:"temporary_disable_minutes"`
}

var defaultMultiKeyAutoDisableSetting = MultiKeyAutoDisableSetting{
	TemporaryStatusCodes:    "429",
	PersistentStatusCodes:   "401",
	TemporaryDisableMinutes: 10,
}

var multiKeyAutoDisableSetting = defaultMultiKeyAutoDisableSetting

func init() {
	config.GlobalConfig.Register("multi_key_auto_disable_setting", &multiKeyAutoDisableSetting)
}

// GetMultiKeyAutoDisableSetting returns a normalized copy of the global per-key policy.
func GetMultiKeyAutoDisableSetting() MultiKeyAutoDisableSetting {
	setting := multiKeyAutoDisableSetting
	temporary, persistent, err := normalizeMultiKeyStatusCodeRules(setting.TemporaryStatusCodes, setting.PersistentStatusCodes)
	if err != nil {
		return defaultMultiKeyAutoDisableSetting
	}
	setting.TemporaryStatusCodes = temporary
	setting.PersistentStatusCodes = persistent
	if setting.TemporaryDisableMinutes < MultiKeyTemporaryDisableMinMinutes || setting.TemporaryDisableMinutes > MultiKeyTemporaryDisableMaxMinutes {
		setting.TemporaryDisableMinutes = defaultMultiKeyAutoDisableSetting.TemporaryDisableMinutes
	}
	return setting
}

// ValidateMultiKeyAutoDisableSetting validates and normalizes one complete policy.
func ValidateMultiKeyAutoDisableSetting(temporaryStatusCodes string, persistentStatusCodes string, disableMinutes int) (MultiKeyAutoDisableSetting, error) {
	temporary, persistent, err := normalizeMultiKeyStatusCodeRules(temporaryStatusCodes, persistentStatusCodes)
	if err != nil {
		return MultiKeyAutoDisableSetting{}, err
	}
	if disableMinutes < MultiKeyTemporaryDisableMinMinutes || disableMinutes > MultiKeyTemporaryDisableMaxMinutes {
		return MultiKeyAutoDisableSetting{}, fmt.Errorf("temporary disable minutes must be between %d and %d", MultiKeyTemporaryDisableMinMinutes, MultiKeyTemporaryDisableMaxMinutes)
	}
	return MultiKeyAutoDisableSetting{
		TemporaryStatusCodes:    temporary,
		PersistentStatusCodes:   persistent,
		TemporaryDisableMinutes: disableMinutes,
	}, nil
}

// NormalizeMultiKeyAutoDisableOption validates one persisted global option.
func NormalizeMultiKeyAutoDisableOption(key string, value string) (string, error) {
	current := GetMultiKeyAutoDisableSetting()
	switch key {
	case "multi_key_auto_disable_setting.temporary_status_codes":
		current.TemporaryStatusCodes = value
	case "multi_key_auto_disable_setting.persistent_status_codes":
		current.PersistentStatusCodes = value
	case "multi_key_auto_disable_setting.temporary_disable_minutes":
		minutes, err := strconv.Atoi(value)
		if err != nil {
			return "", fmt.Errorf("temporary disable minutes must be an integer")
		}
		current.TemporaryDisableMinutes = minutes
	default:
		return value, nil
	}
	normalized, err := ValidateMultiKeyAutoDisableSetting(current.TemporaryStatusCodes, current.PersistentStatusCodes, current.TemporaryDisableMinutes)
	if err != nil {
		return "", err
	}
	switch key {
	case "multi_key_auto_disable_setting.temporary_status_codes":
		return normalized.TemporaryStatusCodes, nil
	case "multi_key_auto_disable_setting.persistent_status_codes":
		return normalized.PersistentStatusCodes, nil
	default:
		return strconv.Itoa(normalized.TemporaryDisableMinutes), nil
	}
}

// MatchMultiKeyStatusCode reports whether one HTTP status matches a normalized rule set.
func MatchMultiKeyStatusCode(rules string, statusCode int) bool {
	ranges, err := ParseHTTPStatusCodeRanges(rules)
	return err == nil && shouldMatchStatusCodeRanges(ranges, statusCode)
}

func normalizeMultiKeyStatusCodeRules(temporaryStatusCodes string, persistentStatusCodes string) (string, string, error) {
	temporary, err := ParseHTTPStatusCodeRanges(temporaryStatusCodes)
	if err != nil {
		return "", "", fmt.Errorf("invalid temporary status codes: %w", err)
	}
	persistent, err := ParseHTTPStatusCodeRanges(persistentStatusCodes)
	if err != nil {
		return "", "", fmt.Errorf("invalid persistent status codes: %w", err)
	}
	for _, temporaryRange := range temporary {
		for _, persistentRange := range persistent {
			if temporaryRange.Start <= persistentRange.End && persistentRange.Start <= temporaryRange.End {
				return "", "", fmt.Errorf("temporary and persistent status codes must not overlap")
			}
		}
	}
	return statusCodeRangesToString(temporary), statusCodeRangesToString(persistent), nil
}
