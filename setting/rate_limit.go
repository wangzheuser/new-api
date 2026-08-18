package setting

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const (
	UserModelRateLimitResponseConfigOption = "UserModelRateLimitResponseConfig"
	UserModelRateLimitDefaultStatusCode    = 429
	UserModelRateLimitDefaultErrorMessage  = "当前请求频率过高，请稍后重试"
	UserModelRateLimitMaxDelaySeconds      = 60
	UserModelRateLimitMaxCount             = 100000000
)

var ModelRequestRateLimitEnabled = false
var ModelRequestRateLimitDurationMinutes = 1
var ModelRequestRateLimitCount = 0
var ModelRequestRateLimitSuccessCount = 1000
var ModelRequestRateLimitGroup = map[string][2]int{}
var ModelRequestRateLimitMutex sync.RWMutex

var userModelRateLimitResponseConfig = defaultUserModelRateLimitResponseConfig()
var userModelRateLimitResponseConfigMutex sync.RWMutex

func ModelRequestRateLimitGroup2JSONString() string {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	jsonBytes, err := common.Marshal(ModelRequestRateLimitGroup)
	if err != nil {
		common.SysLog("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateModelRequestRateLimitGroupByJSONString(jsonStr string) error {
	ModelRequestRateLimitMutex.Lock()
	defer ModelRequestRateLimitMutex.Unlock()

	updated := make(map[string][2]int)
	if err := common.Unmarshal([]byte(jsonStr), &updated); err != nil {
		return err
	}
	ModelRequestRateLimitGroup = updated
	return nil
}

func GetGroupRateLimit(group string) (totalCount, successCount int, found bool) {
	ModelRequestRateLimitMutex.RLock()
	defer ModelRequestRateLimitMutex.RUnlock()

	if ModelRequestRateLimitGroup == nil {
		return 0, 0, false
	}

	limits, found := ModelRequestRateLimitGroup[group]
	if !found {
		return 0, 0, false
	}
	return limits[0], limits[1], true
}

func CheckModelRequestRateLimitGroup(jsonStr string) error {
	checkModelRequestRateLimitGroup := make(map[string][2]int)
	err := common.Unmarshal([]byte(jsonStr), &checkModelRequestRateLimitGroup)
	if err != nil {
		return err
	}
	for group, limits := range checkModelRequestRateLimitGroup {
		if limits[0] < 0 || limits[1] < 1 {
			return fmt.Errorf("group %s has negative rate limit values: [%d, %d]", group, limits[0], limits[1])
		}
		if limits[0] > math.MaxInt32 || limits[1] > math.MaxInt32 {
			return fmt.Errorf("group %s [%d, %d] has max rate limits value 2147483647", group, limits[0], limits[1])
		}
	}

	return nil
}

// defaultUserModelRateLimitResponseConfig returns the built-in response used before any option is saved.
func defaultUserModelRateLimitResponseConfig() dto.UserModelRateLimitResponseConfig {
	return dto.UserModelRateLimitResponseConfig{
		DelaySeconds: 0,
		DefaultResponse: dto.ModelRateLimitResponse{
			StatusCode:   UserModelRateLimitDefaultStatusCode,
			ErrorMessage: UserModelRateLimitDefaultErrorMessage,
		},
		GroupResponses: map[string]dto.ModelRateLimitResponse{},
	}
}

// NormalizeUserModelRateLimitResponseConfig trims and validates a complete response configuration.
func NormalizeUserModelRateLimitResponseConfig(config dto.UserModelRateLimitResponseConfig) (dto.UserModelRateLimitResponseConfig, error) {
	if config.DelaySeconds < 0 || config.DelaySeconds > UserModelRateLimitMaxDelaySeconds {
		return dto.UserModelRateLimitResponseConfig{}, fmt.Errorf("delay_seconds must be between 0 and %d", UserModelRateLimitMaxDelaySeconds)
	}
	defaultResponse, err := NormalizeModelRateLimitResponse(config.DefaultResponse)
	if err != nil {
		return dto.UserModelRateLimitResponseConfig{}, fmt.Errorf("invalid default_response: %w", err)
	}

	normalized := dto.UserModelRateLimitResponseConfig{
		DelaySeconds:    config.DelaySeconds,
		DefaultResponse: defaultResponse,
		GroupResponses:  make(map[string]dto.ModelRateLimitResponse, len(config.GroupResponses)),
	}
	for rawGroup, response := range config.GroupResponses {
		group := strings.TrimSpace(rawGroup)
		if len(group) < 1 || len(group) > 64 {
			return dto.UserModelRateLimitResponseConfig{}, fmt.Errorf("group length must be between 1 and 64")
		}
		if _, exists := normalized.GroupResponses[group]; exists {
			return dto.UserModelRateLimitResponseConfig{}, fmt.Errorf("duplicate group %s", group)
		}
		normalizedResponse, err := NormalizeModelRateLimitResponse(response)
		if err != nil {
			return dto.UserModelRateLimitResponseConfig{}, fmt.Errorf("invalid response for group %s: %w", group, err)
		}
		normalized.GroupResponses[group] = normalizedResponse
	}
	return normalized, nil
}

// NormalizeModelRateLimitResponse validates one public error response.
func NormalizeModelRateLimitResponse(response dto.ModelRateLimitResponse) (dto.ModelRateLimitResponse, error) {
	if response.StatusCode < 400 || response.StatusCode > 599 {
		return dto.ModelRateLimitResponse{}, fmt.Errorf("status_code must be between 400 and 599")
	}
	response.ErrorMessage = strings.TrimSpace(response.ErrorMessage)
	if len(response.ErrorMessage) < 1 || len(response.ErrorMessage) > 512 {
		return dto.ModelRateLimitResponse{}, fmt.Errorf("error_message length must be between 1 and 512")
	}
	return response, nil
}

// CheckUserModelRateLimitResponseConfigJSONString validates an option without changing runtime state.
func CheckUserModelRateLimitResponseConfigJSONString(jsonStr string) error {
	var config dto.UserModelRateLimitResponseConfig
	if err := common.Unmarshal([]byte(jsonStr), &config); err != nil {
		return err
	}
	_, err := NormalizeUserModelRateLimitResponseConfig(config)
	return err
}

// GetUserModelRateLimitResponseConfig returns an isolated copy for request-time resolution.
func GetUserModelRateLimitResponseConfig() dto.UserModelRateLimitResponseConfig {
	userModelRateLimitResponseConfigMutex.RLock()
	defer userModelRateLimitResponseConfigMutex.RUnlock()

	copyConfig := userModelRateLimitResponseConfig
	copyConfig.GroupResponses = make(map[string]dto.ModelRateLimitResponse, len(userModelRateLimitResponseConfig.GroupResponses))
	for group, response := range userModelRateLimitResponseConfig.GroupResponses {
		copyConfig.GroupResponses[group] = response
	}
	return copyConfig
}

// ResolveUserModelRateLimitResponse applies the user, group, then global response hierarchy.
func ResolveUserModelRateLimitResponse(group string, statusCode *int, errorMessage *string) (dto.ModelRateLimitResponse, string, int) {
	config := GetUserModelRateLimitResponseConfig()
	if statusCode != nil && errorMessage != nil {
		response, err := NormalizeModelRateLimitResponse(dto.ModelRateLimitResponse{StatusCode: *statusCode, ErrorMessage: *errorMessage})
		if err == nil {
			return response, "user_group", config.DelaySeconds
		}
		common.SysError("invalid user model rate-limit response override: " + err.Error())
	}
	if response, exists := config.GroupResponses[group]; exists {
		return response, "group", config.DelaySeconds
	}
	return config.DefaultResponse, "global", config.DelaySeconds
}

// UserModelRateLimitResponseConfig2JSONString serializes the current response configuration.
func UserModelRateLimitResponseConfig2JSONString() string {
	config := GetUserModelRateLimitResponseConfig()
	jsonBytes, err := common.Marshal(config)
	if err != nil {
		common.SysError("failed to marshal user model rate-limit response config: " + err.Error())
		fallbackBytes, _ := common.Marshal(defaultUserModelRateLimitResponseConfig())
		return string(fallbackBytes)
	}
	return string(jsonBytes)
}

// UpdateUserModelRateLimitResponseConfigByJSONString validates and installs an option value.
func UpdateUserModelRateLimitResponseConfigByJSONString(jsonStr string) error {
	var config dto.UserModelRateLimitResponseConfig
	if err := common.Unmarshal([]byte(jsonStr), &config); err != nil {
		resetUserModelRateLimitResponseConfig(err)
		return err
	}
	normalized, err := NormalizeUserModelRateLimitResponseConfig(config)
	if err != nil {
		resetUserModelRateLimitResponseConfig(err)
		return err
	}
	userModelRateLimitResponseConfigMutex.Lock()
	userModelRateLimitResponseConfig = normalized
	userModelRateLimitResponseConfigMutex.Unlock()
	return nil
}

// resetUserModelRateLimitResponseConfig installs safe defaults for dirty persisted options.
func resetUserModelRateLimitResponseConfig(cause error) {
	common.SysError("invalid user model rate-limit response config, using built-in defaults: " + cause.Error())
	userModelRateLimitResponseConfigMutex.Lock()
	userModelRateLimitResponseConfig = defaultUserModelRateLimitResponseConfig()
	userModelRateLimitResponseConfigMutex.Unlock()
}
