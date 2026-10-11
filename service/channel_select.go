package service

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func GetChannelConstraints(c *gin.Context) *dto.ChannelConstraints {
	if c == nil {
		return &dto.ChannelConstraints{}
	}
	if existing, ok := common.GetContextKeyType[*dto.ChannelConstraints](c, constant.ContextKeyChannelConstraints); ok && existing != nil {
		return existing
	}
	constraints := &dto.ChannelConstraints{}
	common.SetContextKey(c, constant.ContextKeyChannelConstraints, constraints)
	return constraints
}

func AppendTaskPluginIdentityFilter(c *gin.Context, pluginKey string) {
	if c == nil {
		return
	}
	channelTypes, pluginKeys := pinnedTaskPluginIdentities(c, pluginKey)
	GetChannelConstraints(c).AddFilter(dto.ChannelFilter{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          pluginKey,
		TaskPluginChannelTypes: channelTypes,
		TaskPluginKeys:         pluginKeys,
	})
}

type RetryParam struct {
	Ctx                     *gin.Context
	TokenGroup              string
	ModelName               string
	RequestPath             string
	EndpointType            constant.EndpointType
	RelayFormat             types.RelayFormat
	IsStream                bool
	Retry                   *int
	ExcludedChannelIDs      map[int]struct{}
	PreferredChannelID      int
	ExcludedKeyFingerprints map[int]map[string]struct{}
	resetNextTry            bool
}

// AppendUsedChannel records a channel on the request for diagnostics and
// retry selection. The list is intentionally request-local.
func AppendUsedChannel(c *gin.Context, channelID int) {
	if c == nil || channelID <= 0 {
		return
	}
	used := c.GetStringSlice("used_channels")
	used = append(used, strconv.Itoa(channelID))
	c.Set("used_channels", used)
}

// ExcludeChannelKey prevents one request from selecting the same failed credential again.
func (p *RetryParam) ExcludeChannelKey(channelId int, key string) {
	if p == nil || channelId <= 0 || key == "" {
		return
	}
	if p.ExcludedKeyFingerprints == nil {
		p.ExcludedKeyFingerprints = make(map[int]map[string]struct{})
	}
	if p.ExcludedKeyFingerprints[channelId] == nil {
		p.ExcludedKeyFingerprints[channelId] = make(map[string]struct{})
	}
	p.ExcludedKeyFingerprints[channelId][MultiKeyFingerprint(key)] = struct{}{}
}

// ExcludedChannelKeys returns the request-local credential exclusions for one channel.
func (p *RetryParam) ExcludedChannelKeys(channelId int) map[string]struct{} {
	if p == nil || p.ExcludedKeyFingerprints == nil {
		return nil
	}
	return p.ExcludedKeyFingerprints[channelId]
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) SetRetry(retry int) {
	p.Retry = &retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.resetNextTry {
		p.resetNextTry = false
		return
	}
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

func (p *RetryParam) ResetRetryNextTry() {
	p.resetNextTry = true
}

// CacheGetRandomSatisfiedChannel tries to get a random channel that satisfies the requirements.
// 尝试获取一个满足要求的随机渠道。
//
// For "auto" tokenGroup with cross-group Retry enabled:
// 对于启用了跨分组重试的 "auto" tokenGroup：
//
//   - Each group will exhaust all its priorities before moving to the next group.
//     每个分组会用完所有优先级后才会切换到下一个分组。
//
//   - Uses ContextKeyAutoGroupIndex to track current group index.
//     使用 ContextKeyAutoGroupIndex 跟踪当前分组索引。
//
//   - Uses ContextKeyAutoGroupRetryIndex to track the global Retry count when current group started.
//     使用 ContextKeyAutoGroupRetryIndex 跟踪当前分组开始时的全局重试次数。
//
//   - priorityRetry = Retry - startRetryIndex, represents the priority level within current group.
//     priorityRetry = Retry - startRetryIndex，表示当前分组内的优先级级别。
//
//   - When GetRandomSatisfiedChannel returns nil (priorities exhausted), moves to next group.
//     当 GetRandomSatisfiedChannel 返回 nil（优先级用完）时，切换到下一个分组。
//
// Example flow (2 groups, each with 2 priorities, RetryTimes=3):
// 示例流程（2个分组，每个有2个优先级，RetryTimes=3）：
//
//	Retry=0: GroupA, priority0 (startRetryIndex=0, priorityRetry=0)
//	         分组A, 优先级0
//
//	Retry=1: GroupA, priority1 (startRetryIndex=0, priorityRetry=1)
//	         分组A, 优先级1
//
//	Retry=2: GroupA exhausted → GroupB, priority0 (startRetryIndex=2, priorityRetry=0)
//	         分组A用完 → 分组B, 优先级0
//
//	Retry=3: GroupB, priority1 (startRetryIndex=2, priorityRetry=1)
//	         分组B, 优先级1
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	var channel *model.Channel
	var err error
	selectGroup := param.TokenGroup
	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)
	filters := GetChannelConstraints(param.Ctx).Filters
	usedChannelIds := make(map[int]struct{})
	for _, rawChannelId := range param.Ctx.GetStringSlice("use_channel") {
		if channelId, parseErr := strconv.Atoi(rawChannelId); parseErr == nil {
			usedChannelIds[channelId] = struct{}{}
		}
	}

	if param.TokenGroup == "auto" {
		autoGroups := GetRequestAutoGroups(param.Ctx, userGroup)
		if len(autoGroups) == 0 {
			return nil, selectGroup, errors.New("auto groups is not enabled")
		}
		userId := common.GetContextKeyInt(param.Ctx, constant.ContextKeyUserId)
		// Token-scoped auto groups are authoritative when present; still enforce
		// the user's effective-group permissions before selecting a channel.
		if _, hasTokenGroups := common.GetContextKey(param.Ctx, constant.ContextKeyTokenAutoGroups); !hasTokenGroups {
			var groupErr error
			autoGroups, groupErr = GetUserEffectiveAutoGroups(userId, userGroup)
			if groupErr != nil {
				return nil, selectGroup, groupErr
			}
		}

		// startGroupIndex: the group index to start searching from
		// startGroupIndex: 开始搜索的分组索引
		startGroupIndex := 0
		crossGroupRetry := common.GetContextKeyBool(param.Ctx, constant.ContextKeyTokenCrossGroupRetry)

		if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if idx, ok := lastGroupIndex.(int); ok {
				startGroupIndex = idx
			}
		}

		for i := startGroupIndex; i < len(autoGroups); i++ {
			autoGroup := autoGroups[i]
			// Calculate priorityRetry for current group
			// 计算当前分组的 priorityRetry
			priorityRetry := param.GetRetry()
			// If moved to a new group, reset priorityRetry and update startRetryIndex
			// 如果切换到新分组，重置 priorityRetry 并更新 startRetryIndex
			if i > startGroupIndex {
				priorityRetry = 0
			}
			logger.LogDebug(param.Ctx, "Auto selecting group: %s, priorityRetry: %d", autoGroup, priorityRetry)

			channel, _ = model.GetRandomSatisfiedChannel(autoGroup, param.ModelName, priorityRetry, param.RequestPath, usedChannelIds, param.ExcludedChannelIDs, filters)
			if channel == nil {
				// Current group has no available channel for this model, try next group
				// 当前分组没有该模型的可用渠道，尝试下一个分组
				logger.LogDebug(param.Ctx, "No available channel in group %s for model %s at priorityRetry %d, trying next group", autoGroup, param.ModelName, priorityRetry)
				// 重置状态以尝试下一个分组
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
				// Reset retry counter so outer loop can continue for next group
				// 重置重试计数器，以便外层循环可以为下一个分组继续
				param.SetRetry(0)
				continue
			}
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
			selectGroup = autoGroup
			logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)

			// Prepare state for next retry
			// 为下一次重试准备状态
			if crossGroupRetry && priorityRetry >= common.RetryTimes {
				// Current group has exhausted all retries, prepare to switch to next group
				// This request still uses current group, but next retry will use next group
				// 当前分组已用完所有重试次数，准备切换到下一个分组
				// 本次请求仍使用当前分组，但下次重试将使用下一个分组
				logger.LogDebug(param.Ctx, "Current group %s retries exhausted (priorityRetry=%d >= RetryTimes=%d), preparing switch to next group for next retry", autoGroup, priorityRetry, common.RetryTimes)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				// Reset retry counter so outer loop can continue for next group
				// 重置重试计数器，以便外层循环可以为下一个分组继续
				param.SetRetry(0)
				param.ResetRetryNextTry()
			} else {
				// Stay in current group, save current state
				// 保持在当前分组，保存当前状态
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
			}
			break
		}
	} else {
		channel, err = model.GetRandomSatisfiedChannel(param.TokenGroup, param.ModelName, param.GetRetry(), param.RequestPath, usedChannelIds, param.ExcludedChannelIDs, filters)
		if err != nil {
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}

// ChannelSelectError explains why a request could not select a channel.
type ChannelSelectError struct {
	StatusCode         int
	Code               types.ErrorCode
	MessageID          string
	Params             map[string]any
	Message            string
	FilterKind         dto.ChannelFilterKind
	Channel            *model.Channel
	NoAvailableChannel bool
}

// SelectChannelForRequest applies pins, session affinity and request filters before random selection.
func SelectChannelForRequest(c *gin.Context, modelName string, retry *RetryParam) (*model.Channel, string, *ChannelSelectError) {
	constraints := GetChannelConstraints(c)
	if pin, found, overridden := constraints.ResolvedPin(); found {
		for _, lost := range overridden {
			logger.LogWarn(c, fmt.Sprintf("channel pin overridden: winning_source=%s winning_channel_id=%d overridden_source=%s overridden_channel_id=%d", pin.Source, pin.ChannelId, lost.Source, lost.ChannelId))
		}
		channel, err := model.CacheGetChannel(pin.ChannelId)
		if err != nil {
			return nil, "", pinnedChannelUnavailable(pin, http.StatusBadRequest, i18n.MsgDistributorInvalidChannelId)
		}
		if channel.Status != common.ChannelStatusEnabled {
			return nil, "", pinnedChannelUnavailable(pin, http.StatusForbidden, i18n.MsgDistributorChannelDisabled)
		}
		if ok, kind := model.ChannelSatisfiesFilters(channel, modelName, constraints.Filters); !ok {
			return nil, "", &ChannelSelectError{StatusCode: http.StatusBadRequest, Code: types.ErrorCode(kind), MessageID: i18n.MsgDistributorNoAvailableChannel, Params: map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": modelName}, FilterKind: kind, Channel: channel}
		}
		return channel, "", nil
	}

	usingGroup := retry.TokenGroup
	var channel *model.Channel
	selectGroup := ""
	if retry.GetRetry() == 0 {
		if preferredID, found := GetPreferredChannelByAffinity(c, modelName, usingGroup); found {
			preferred, err := model.CacheGetChannel(preferredID)
			usable := err == nil && preferred != nil && preferred.Status == common.ChannelStatusEnabled
			if usable {
				usable, _ = model.ChannelSatisfiesFilters(preferred, modelName, constraints.Filters)
			}
			if usable {
				if usingGroup == "auto" {
					userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
					for _, group := range GetRequestAutoGroups(c, userGroup) {
						if model.IsChannelEnabledForGroupModel(group, modelName, preferred.Id) {
							selectGroup, channel = group, preferred
							common.SetContextKey(c, constant.ContextKeyAutoGroup, group)
							MarkChannelAffinityUsed(c, group, preferred.Id)
							break
						}
					}
				} else if model.IsChannelEnabledForGroupModel(usingGroup, modelName, preferred.Id) {
					selectGroup, channel = usingGroup, preferred
					MarkChannelAffinityUsed(c, usingGroup, preferred.Id)
				}
			}
			if channel == nil {
				if !ShouldKeepChannelAffinityOnChannelDisabled() {
					ClearCurrentChannelAffinityCache(c)
				}
				if RequestPolicy(c).SessionMode == "strict" {
					return nil, "", &ChannelSelectError{StatusCode: http.StatusServiceUnavailable, Message: "strict_session_binding_unavailable"}
				}
			}
		}
	}
	if channel == nil {
		var err error
		channel, selectGroup, err = CacheGetRandomSatisfiedChannel(retry)
		if err != nil {
			showGroup := usingGroup
			if usingGroup == "auto" {
				showGroup = fmt.Sprintf("auto(%s)", selectGroup)
			}
			return nil, selectGroup, &ChannelSelectError{StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorGetChannelFailed, Params: map[string]any{"Group": showGroup, "Model": modelName, "Error": err.Error()}}
		}
		if channel == nil {
			return nil, selectGroup, &ChannelSelectError{StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorNoAvailableChannel, Params: map[string]any{"Group": usingGroup, "Model": modelName}, NoAvailableChannel: true}
		}
	}
	if ok, kind := model.ChannelSatisfiesFilters(channel, modelName, constraints.Filters); !ok {
		return nil, selectGroup, &ChannelSelectError{StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorNoAvailableChannel, Params: map[string]any{"Group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup), "Model": modelName}, FilterKind: kind, Channel: channel, NoAvailableChannel: true}
	}
	return channel, selectGroup, nil
}

func pinnedChannelUnavailable(pin dto.ChannelPin, statusCode int, messageID string) *ChannelSelectError {
	if pin.Source == dto.PinSourceOriginTask {
		return &ChannelSelectError{StatusCode: http.StatusBadRequest, Code: "origin_task_channel_disabled", Message: "origin_task_channel_disabled"}
	}
	return &ChannelSelectError{StatusCode: statusCode, MessageID: messageID}
}

func pinnedTaskPluginIdentities(c *gin.Context, expected string) ([]int, []string) {
	if c == nil || expected == "" {
		return nil, nil
	}
	if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
		pinned, ok := value.(jsplugin.PinnedEndpoint)
		if ok && pinned.Generation != nil && len(pinned.Candidates) > 1 {
			expectedFound := false
			channelTypes := make([]int, 0, len(pinned.Candidates))
			pluginKeys := make([]string, 0, len(pinned.Candidates))
			seen := make(map[int]struct{}, len(pinned.Candidates))
			for _, candidate := range pinned.Candidates {
				if candidate.Plugin == nil {
					continue
				}
				if candidate.Plugin.Meta.Key == expected {
					expectedFound = true
				}
				pluginKeys = append(pluginKeys, candidate.Plugin.Meta.Key)
				for _, channelType := range candidate.Plugin.Meta.ChannelTypes {
					if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
						continue
					}
					if _, duplicate := seen[channelType]; duplicate {
						continue
					}
					if plugin, indexed := pinned.Generation.GetByChannelType(channelType); indexed && plugin == candidate.Plugin {
						seen[channelType] = struct{}{}
						channelTypes = append(channelTypes, channelType)
					}
				}
			}
			if expectedFound {
				return channelTypes, pluginKeys
			}
		}
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, ok := value.(jsplugin.PinnedPlugin)
	if !exists || !ok || pinned.Generation == nil || pinned.Plugin == nil || pinned.Plugin.Meta.Key != expected {
		return nil, nil
	}
	channelTypes := make([]int, 0, len(pinned.Plugin.Meta.ChannelTypes))
	for _, channelType := range pinned.Plugin.Meta.ChannelTypes {
		if channelType == 0 || channelType == constant.ChannelTypeTaskPlugin {
			continue
		}
		channelTypes = append(channelTypes, channelType)
	}
	return channelTypes, []string{expected}
}

// CacheGetRandomSatisfiedChannelWithRoute selects a channel and returns its protocol plan.
func CacheGetRandomSatisfiedChannelWithRoute(param *RetryParam) (*model.Channel, *types.ChannelRoutePlan, string, error) {
	if param == nil {
		return nil, nil, "", errors.New("retry parameters are required")
	}
	endpointType, relayFormat, _, isTextProtocol := ResolveClientTextProtocol(param.RequestPath)
	if param.EndpointType == "" {
		param.EndpointType = endpointType
	}
	if param.RelayFormat == "" {
		param.RelayFormat = relayFormat
	}

	for {
		channel, selectGroup, err := CacheGetRandomSatisfiedChannel(param)
		if err != nil {
			return channel, nil, selectGroup, err
		}
		if channel == nil {
			return nil, nil, selectGroup, nil
		}
		mappingModel := param.ModelName
		if strings.HasSuffix(param.RequestPath, "/responses/compact") {
			mappingModel = strings.TrimSuffix(mappingModel, ratio_setting.CompactModelSuffix)
		}
		_, mappingErr := common.ResolveMappedModel(channel.GetModelMapping(), mappingModel)
		if mappingErr != nil {
			return nil, nil, selectGroup, mappingErr
		}
		if IsChannelRoutingBlocked(channel, mappingModel) {
			if param.ExcludedChannelIDs == nil {
				param.ExcludedChannelIDs = make(map[int]struct{})
			}
			param.ExcludedChannelIDs[channel.Id] = struct{}{}
			continue
		}
		if !isTextProtocol {
			return channel, nil, selectGroup, nil
		}
		plan, planErr := PlanChannelProtocolRoute(channel, param.ModelName, param.RequestPath, param.IsStream)
		if planErr != nil {
			return nil, nil, selectGroup, planErr
		}
		if plan != nil {
			return channel, plan, selectGroup, nil
		}

		// Protocol-incompatible candidates are excluded only for this request.
		if param.ExcludedChannelIDs == nil {
			param.ExcludedChannelIDs = make(map[int]struct{})
		}
		param.ExcludedChannelIDs[channel.Id] = struct{}{}
	}
}
