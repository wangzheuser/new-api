package middleware

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type ModelRequest struct {
	Model  string `json:"model"`
	Group  string `json:"group,omitempty"`
	Stream bool   `json:"stream,omitempty"`
}

// TokenModelLimitAllows applies the same alias and modifier matching rules as
// the HTTP distributor while preserving explicitly blacklisted full IDs.
func TokenModelLimitAllows(limit map[string]bool, modelName string) bool {
	if len(limit) == 0 || modelName == "" {
		return false
	}
	if limit[modelName] {
		return true
	}
	if model_setting.ShouldPreserveThinkingSuffix(modelName) {
		return false
	}
	base := ratio_setting.FormatMatchingModelName(reasoning.BaseModelName(modelName))
	return limit[base]
}

func Distribute() func(c *gin.Context) {
	return func(c *gin.Context) {
		var channel *model.Channel
		var selectErr *service.ChannelSelectError
		defer func() {
			if c.Writer.Status() >= 400 {
				service.RecordRequestPolicyTermination(c, types.NewErrorWithStatusCode(errors.New("request rejected"), types.ErrorCodeInvalidRequest, c.Writer.Status(), types.ErrOptionWithSkipRetry()))
			}
		}()
		constraints := service.GetChannelConstraints(c)
		constraints.AddFilter(dto.ChannelFilter{Kind: dto.FilterRequestPath, RequestPath: c.Request.URL.Path})
		service.AppendTaskPluginIdentityFilter(c, c.GetString("expected_task_plugin_key"))
		modelRequest, shouldSelectChannel, err := getModelRequest(c)
		if err != nil {
			abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
			return
		}
		if invalidStream, exists := c.Get("invalid_stream_flag_error"); exists {
			if streamErr, ok := invalidStream.(error); ok {
				abortWithOpenAiMessage(c, http.StatusBadRequest, streamErr.Error())
				return
			}
		}
		if strings.Contains(c.Request.URL.Path, ":streamGenerateContent") {
			modelRequest.Stream = true
		}
		common.SetContextKey(c, constant.ContextKeyIsStream, modelRequest.Stream)
		_, pinned, _ := constraints.ResolvedPin()
		if !pinned {
			modelLimitEnable := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
			if modelLimitEnable {
				s, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
				if !ok {
					abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenNoModelAccess))
					return
				}
				tokenModelLimit, ok := s.(map[string]bool)
				if !ok {
					tokenModelLimit = map[string]bool{}
				}
				if !TokenModelLimitAllows(tokenModelLimit, modelRequest.Model) {
					abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorTokenModelForbidden, map[string]any{"Model": modelRequest.Model}))
					return
				}
			}

			if shouldSelectChannel && modelRequest.Model == "" {
				abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorModelNameRequired))
				return
			}
			if shouldSelectChannel && strings.HasPrefix(c.Request.URL.Path, "/pg/chat/completions") {
				playgroundRequest := &dto.PlayGroundRequest{}
				if err = common.UnmarshalBodyReusable(c, playgroundRequest); err != nil {
					abortWithOpenAiMessage(c, http.StatusBadRequest, i18n.T(c, i18n.MsgDistributorInvalidPlayground, map[string]any{"Error": err.Error()}))
					return
				}
				if playgroundRequest.Group != "" {
					userId := common.GetContextKeyInt(c, constant.ContextKeyUserId)
					userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
					var allowed bool
					var groupErr error
					if playgroundRequest.Group == "auto" {
						var autoGroups []string
						autoGroups, groupErr = service.GetUserEffectiveAutoGroups(userId, userGroup)
						allowed = len(autoGroups) > 0
					} else {
						allowed, groupErr = service.IsUserSelectableEffectiveGroup(userId, userGroup, playgroundRequest.Group)
					}
					if groupErr != nil {
						abortWithOpenAiMessage(c, http.StatusInternalServerError, i18n.T(c, i18n.MsgDatabaseError))
						return
					}
					if !allowed {
						abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgDistributorGroupAccessDenied))
						return
					}
					common.SetContextKey(c, constant.ContextKeyUsingGroup, playgroundRequest.Group)
				}
			}
		}
		if pinned || shouldSelectChannel {
			usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
			channel, _, selectErr = service.SelectChannelForRequest(c, modelRequest.Model, &service.RetryParam{
				Ctx: c, ModelName: modelRequest.Model, TokenGroup: usingGroup,
				RequestPath: c.Request.URL.Path, IsStream: modelRequest.Stream, Retry: common.GetPointer(0),
			})
			if selectErr != nil {
				if selectErr.FilterKind == dto.FilterTaskPluginIdentity {
					logTaskPluginChannelDecision(c, selectErr.Channel, modelRequest.Model, "channel_rejected", "identity_mismatch")
				}
				message := selectErr.Message
				if selectErr.NoAvailableChannel {
					message = noAvailableChannelMessage(c, usingGroup, modelRequest.Model)
				} else if selectErr.MessageID != "" {
					message = i18n.T(c, selectErr.MessageID, selectErr.Params)
				}
				abortWithOpenAiMessage(c, selectErr.StatusCode, message, selectErr.Code)
				return
			}
		}
		common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
		defer service.FinishChannelKeyProbe(c, false)
		retryParam := &service.RetryParam{
			Ctx:                c,
			ModelName:          modelRequest.Model,
			TokenGroup:         common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
			RequestPath:        c.Request.URL.Path,
			IsStream:           modelRequest.Stream,
			Retry:              common.GetPointer(0),
			ExcludedChannelIDs: make(map[int]struct{}),
		}
		for {
			setupErr := SetupContextForSelectedChannel(c, channel, modelRequest.Model)
			if setupErr == nil {
				break
			}
			_, pinned, _ := constraints.ResolvedPin()
			failedChannel := channel
			if setupErr.GetErrorCode() == types.ErrorCodeChannelNoAvailableKey && failedChannel != nil && !pinned &&
				!service.ShouldSkipRetryAfterChannelAffinityFailure(c) && retryParam.GetRetry() < common.RetryTimes {
				retryParam.ExcludedChannelIDs[failedChannel.Id] = struct{}{}
				retryParam.IncreaseRetry()
				channel, _, selectErr = service.SelectChannelForRequest(c, modelRequest.Model, retryParam)
				if selectErr == nil {
					continue
				}
			}
			recordDistributorSetupError(c, failedChannel, setupErr)
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, setupErr.Error(), types.ErrorCodeModelNotFound)
			return
		}
		c.Next()
		upstreamSucceeded := common.GetContextKeyBool(c, constant.ContextKeyRelayUpstreamSucceeded)
		clientStatus := 0
		if c.Writer != nil {
			clientStatus = c.Writer.Status()
		}
		if channel != nil && shouldRecordChannelAffinity(upstreamSucceeded, clientStatus) {
			service.RecordChannelAffinity(c, channel.Id)
		}
	}
}

// noAvailableChannelMessage explains a task-plugin routing miss without exposing plugin keys.
func noAvailableChannelMessage(c *gin.Context, group, modelName string) string {
	value, exists := c.Get(jsplugin.ContextKeyPinnedPlugin)
	pinned, ok := value.(jsplugin.PinnedPlugin)
	if exists && ok && pinned.Plugin != nil {
		keys := []string{pinned.Plugin.Meta.Key}
		if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
			if endpoint, ok := value.(jsplugin.PinnedEndpoint); ok && len(endpoint.Candidates) > 0 {
				keys = nil
				for _, candidate := range endpoint.Candidates {
					if candidate.Plugin != nil {
						keys = append(keys, candidate.Plugin.Meta.Key)
					}
				}
			}
		}
		logger.LogWarn(c, common.LogText("task_plugin subsystem=distribution event=no_available_channel group=%q model=%q plugins=%q reason=no_eligible_channel", group, modelName, strings.Join(keys, ",")))
		return i18n.T(c, i18n.MsgDistributorNoAvailableChannelTaskPlugin, map[string]any{"Group": group, "Model": modelName})
	}
	return i18n.T(c, i18n.MsgDistributorNoAvailableChannel, map[string]any{"Group": group, "Model": modelName})
}

// shouldRecordChannelAffinity keeps internal relay success independent from a
// client-facing error produced by a response override.
func shouldRecordChannelAffinity(upstreamSucceeded bool, clientStatus int) bool {
	return upstreamSucceeded || (clientStatus > 0 && clientStatus < http.StatusBadRequest)
}

// channelSupportsRequestPath reports whether a channel can serve the request path.
// Only Advanced Custom (type 58) channels are path-checked; all other channel types
// always pass. A type-58 channel is usable only when one of its routes matches.
func channelSupportsRequestPath(channel *model.Channel, requestPath string, requestModel string) bool {
	if channel == nil {
		return false
	}
	if channel.Type != constant.ChannelTypeAdvancedCustom {
		return true
	}
	config := channel.GetOtherSettings().AdvancedCustom
	return config != nil && config.SupportsPathForModel(requestPath, requestModel)
}

// channelSupportsRequest applies explicit Advanced Custom routing and protocol policy checks.
func channelSupportsRequest(channel *model.Channel, requestPath string, requestModel string, stream bool) bool {
	if !channelSupportsRequestPath(channel, requestPath, requestModel) {
		return false
	}
	plan, err := service.PlanChannelProtocolRoute(channel, requestModel, requestPath, stream)
	if err != nil {
		return false
	}
	_, _, _, isTextProtocol := service.ResolveClientTextProtocol(requestPath)
	return !isTextProtocol || plan != nil
}

// getModelFromRequest 从请求中读取模型信息
// 根据 Content-Type 自动处理：
// - application/json
// - application/x-www-form-urlencoded
// - multipart/form-data
func getModelFromRequest(c *gin.Context) (*ModelRequest, error) {
	if cached, exists := c.Get(contextKeyTaskPluginEndpointModel); exists {
		if modelRequest, ok := cached.(ModelRequest); ok {
			cachedRequest := modelRequest
			return &cachedRequest, nil
		}
	}
	if strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		modelRequest, err := getModelFromJSONBody(c)
		if err != nil {
			return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
		}
		return modelRequest, nil
	}

	var modelRequest ModelRequest
	err := common.UnmarshalBodyReusable(c, &modelRequest)
	if err != nil {
		return nil, errors.New(i18n.T(c, i18n.MsgDistributorInvalidRequest, map[string]any{"Error": err.Error()}))
	}
	return &modelRequest, nil
}

func getModelFromJSONBody(c *gin.Context) (*ModelRequest, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	requestBody, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	if !gjson.ValidBytes(requestBody) {
		return nil, errors.New("invalid JSON request body")
	}
	if countTopLevelJSONKey(requestBody, "model") > 1 {
		return nil, errors.New("model must be provided once")
	}

	values := gjson.GetManyBytes(requestBody, "model", "group", "stream")
	model, err := getJSONStringValue(values[0], "model")
	if err != nil {
		return nil, err
	}
	group, err := getJSONStringValue(values[1], "group")
	if err != nil {
		return nil, err
	}
	stream, streamErr := getJSONBoolValue(values[2], "stream")
	if streamErr != nil {
		// Keep model discovery available to task-plugin preparation so it can
		// return the protocol-specific validation error.
		c.Set("invalid_stream_flag_error", streamErr)
	}

	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return nil, seekErr
	}
	c.Request.Body = io.NopCloser(storage)

	return &ModelRequest{
		Model:  model,
		Group:  group,
		Stream: stream,
	}, nil
}

func countTopLevelJSONKey(data []byte, target string) int {
	depth := 0
	inString := false
	escaped := false
	stringStart := 0
	expectingKey := false
	count := 0
	for index, current := range data {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current != '"' {
				continue
			}
			inString = false
			if depth == 1 && expectingKey {
				key := string(data[stringStart:index])
				var decodedKey string
				if common.Unmarshal(data[stringStart-1:index+1], &decodedKey) == nil {
					key = decodedKey
				}
				cursor := index + 1
				for cursor < len(data) && (data[cursor] == ' ' || data[cursor] == '\t' || data[cursor] == '\r' || data[cursor] == '\n') {
					cursor++
				}
				if cursor < len(data) && data[cursor] == ':' && key == target {
					count++
				}
				expectingKey = false
			}
			continue
		}
		switch current {
		case '"':
			inString = true
			stringStart = index + 1
		case '{':
			depth++
			if depth == 1 {
				expectingKey = true
			}
		case '}':
			depth--
		case ',':
			if depth == 1 {
				expectingKey = true
			}
		}
	}
	return count
}

func getJSONStringValue(result gjson.Result, field string) (string, error) {
	if !result.Exists() || result.Type == gjson.Null {
		return "", nil
	}
	if result.Type != gjson.String {
		return "", fmt.Errorf("field %s must be a string", field)
	}
	return result.String(), nil
}

func getJSONBoolValue(result gjson.Result, field string) (bool, error) {
	if !result.Exists() || result.Type == gjson.Null {
		return false, nil
	}
	if result.Type != gjson.True && result.Type != gjson.False {
		return false, fmt.Errorf("field %s must be a boolean", field)
	}
	return result.Bool(), nil
}

func getModelRequest(c *gin.Context) (*ModelRequest, bool, error) {
	var modelRequest ModelRequest
	shouldSelectChannel := true
	var err error
	if modelName := c.GetString("resolved_task_model"); modelName != "" {
		modelRequest.Model = modelName
	} else if strings.Contains(c.Request.URL.Path, "/mj/") {
		relayMode := relayconstant.Path2RelayModeMidjourney(c.Request.URL.Path)
		if relayMode == relayconstant.RelayModeMidjourneyTaskFetch ||
			relayMode == relayconstant.RelayModeMidjourneyTaskFetchByCondition ||
			relayMode == relayconstant.RelayModeMidjourneyNotify ||
			relayMode == relayconstant.RelayModeMidjourneyTaskImageSeed {
			shouldSelectChannel = false
		} else {
			midjourneyRequest := dto.MidjourneyRequest{}
			err = common.UnmarshalBodyReusable(c, &midjourneyRequest)
			if err != nil {
				return nil, false, errors.New(i18n.T(c, i18n.MsgDistributorInvalidMidjourney, map[string]any{"Error": err.Error()}))
			}
			midjourneyModel, mjErr, success := service.GetMjRequestModel(relayMode, &midjourneyRequest)
			if mjErr != nil {
				return nil, false, fmt.Errorf("%s", mjErr.Description)
			}
			if midjourneyModel == "" {
				if !success {
					return nil, false, fmt.Errorf("%s", i18n.T(c, i18n.MsgDistributorInvalidParseModel))
				} else {
					// task fetch, task fetch by condition, notify
					shouldSelectChannel = false
				}
			}
			modelRequest.Model = midjourneyModel
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/suno/") {
		relayMode := relayconstant.Path2RelaySuno(c.Request.Method, c.Request.URL.Path)
		if relayMode == relayconstant.RelayModeSunoFetch ||
			relayMode == relayconstant.RelayModeSunoFetchByID {
			shouldSelectChannel = false
		} else {
			modelName := service.CoverTaskActionToModelName(constant.TaskPlatformSuno, c.Param("action"))
			modelRequest.Model = modelName
		}
		c.Set("platform", string(constant.TaskPlatformSuno))
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/videos/") && strings.HasSuffix(c.Request.URL.Path, "/remix") {
		relayMode := relayconstant.RelayModeVideoSubmit
		c.Set("relay_mode", relayMode)
		shouldSelectChannel = false
	} else if strings.Contains(c.Request.URL.Path, "/v1/videos") {
		//curl https://api.openai.com/v1/videos \
		//  -H "Authorization: Bearer $OPENAI_API_KEY" \
		//  -F "model=sora-2" \
		//  -F "prompt=A calico cat playing a piano on stage"
		//	-F input_reference="@image.jpg"
		relayMode := relayconstant.RelayModeUnknown
		if c.Request.Method == http.MethodPost {
			relayMode = relayconstant.RelayModeVideoSubmit
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			if req != nil {
				modelRequest.Model = req.Model
			}
		} else if c.Request.Method == http.MethodGet {
			relayMode = relayconstant.RelayModeVideoFetchByID
			shouldSelectChannel = false
			modelRequest.Model = getTaskOriginModelName(c)
		}
		c.Set("relay_mode", relayMode)
	} else if strings.Contains(c.Request.URL.Path, "/v1/video/generations") {
		relayMode := relayconstant.RelayModeUnknown
		if c.Request.Method == http.MethodPost {
			req, err := getModelFromRequest(c)
			if err != nil {
				return nil, false, err
			}
			modelRequest.Model = req.Model
			relayMode = relayconstant.RelayModeVideoSubmit
		} else if c.Request.Method == http.MethodGet {
			relayMode = relayconstant.RelayModeVideoFetchByID
			shouldSelectChannel = false
			modelRequest.Model = getTaskOriginModelName(c)
		}
		if _, ok := c.Get("relay_mode"); !ok {
			c.Set("relay_mode", relayMode)
		}
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1beta/models/") || strings.HasPrefix(c.Request.URL.Path, "/v1/models/") {
		// Gemini API 路径处理: /v1beta/models/gemini-2.0-flash:generateContent
		relayMode := relayconstant.RelayModeGemini
		modelName := extractModelNameFromGeminiPath(c.Request.URL.Path)
		if modelName != "" {
			modelRequest.Model = modelName
		}
		c.Set("relay_mode", relayMode)
	} else if !strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") && !strings.Contains(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		req, err := getModelFromRequest(c)
		if err != nil {
			return nil, false, err
		}
		modelRequest.Model = req.Model
		modelRequest.Stream = req.Stream
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/realtime") {
		//wss://api.openai.com/v1/realtime?model=gpt-4o-realtime-preview-2024-10-01
		modelRequest.Model = c.Query("model")
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/moderations") {
		if modelRequest.Model == "" {
			modelRequest.Model = "text-moderation-stable"
		}
	}
	if strings.HasSuffix(c.Request.URL.Path, "embeddings") {
		if modelRequest.Model == "" {
			modelRequest.Model = c.Param("model")
		}
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/images/generations") {
		modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "dall-e")
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1/images/edits") {
		//modelRequest.Model = common.GetStringIfEmpty(c.PostForm("model"), "gpt-image-1")
		contentType := c.ContentType()
		if slices.Contains([]string{gin.MIMEPOSTForm, gin.MIMEMultipartPOSTForm}, contentType) {
			req, err := getModelFromRequest(c)
			if err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
		}
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/audio") {
		relayMode := relayconstant.RelayModeAudioSpeech
		if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/speech") {

			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "tts-1")
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/translations") {
			// 先尝试从请求读取
			if req, err := getModelFromRequest(c); err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "whisper-1")
			relayMode = relayconstant.RelayModeAudioTranslation
		} else if strings.HasPrefix(c.Request.URL.Path, "/v1/audio/transcriptions") {
			// 先尝试从请求读取
			if req, err := getModelFromRequest(c); err == nil && req.Model != "" {
				modelRequest.Model = req.Model
			}
			modelRequest.Model = common.GetStringIfEmpty(modelRequest.Model, "whisper-1")
			relayMode = relayconstant.RelayModeAudioTranscription
		}
		c.Set("relay_mode", relayMode)
	}
	if strings.HasPrefix(c.Request.URL.Path, "/pg/chat/completions") {
		// playground chat completions
		req, err := getModelFromRequest(c)
		if err != nil {
			return nil, false, err
		}
		modelRequest.Model = req.Model
		modelRequest.Group = req.Group
		common.SetContextKey(c, constant.ContextKeyTokenGroup, modelRequest.Group)
	}

	if strings.HasPrefix(c.Request.URL.Path, "/v1/responses/compact") && modelRequest.Model != "" {
		modelRequest.Model = ratio_setting.WithCompactModelSuffix(modelRequest.Model)
	}
	return &modelRequest, shouldSelectChannel, nil
}

// 修复 #4834: GET /v1/video/generations/:task_id && /v1/video/:task_id 此前不解析 model，
// 当 token 启用「可用模型限制」时，下游 modelLimitEnable 校验会因
// modelRequest.Model 为空而误报 "This token has no access to model"。
// 从已存储的任务记录中回填 OriginModelName 即可让校验走在正确的模型上。
func getTaskOriginModelName(c *gin.Context) string {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		return ""
	}

	taskId := c.Param("task_id")
	if taskId == "" {
		// jimeng adapter
		taskId = c.GetString("task_id")
	}
	if taskId == "" {
		return ""
	}

	userId := c.GetInt("id")
	if task, exist, err := model.GetByTaskId(userId, taskId); err == nil && exist && task != nil {
		return task.Properties.OriginModelName
	}
	return ""
}

func SetupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string) *types.NewAPIError {
	return setupContextForSelectedChannel(c, channel, modelName, nil, nil)
}

func channelMatchesExpectedTaskPlugin(c *gin.Context, channel *model.Channel, expected string) bool {
	if channel == nil {
		return false
	}
	if c != nil {
		if _, matched := pinnedEndpointCandidateForChannel(c, channel, expected); matched {
			return true
		}
	}
	if expected == "" {
		return channel.Type != constant.ChannelTypeTaskPlugin
	}
	if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
		return channel.GetSetting().BindsTaskPlugin(expected)
	}
	if c == nil {
		return false
	}
	if pinnedValue, ok := c.Get(jsplugin.ContextKeyPinnedPlugin); ok {
		pinned, ok := pinnedValue.(jsplugin.PinnedPlugin)
		return ok && pinned.Plugin != nil && pinned.Plugin.Meta.Key == expected && slices.Contains(pinned.Plugin.Meta.ChannelTypes, channel.Type)
	}
	return false
}

func pinnedEndpointCandidateForChannel(c *gin.Context, channel *model.Channel, expected string) (jsplugin.ProtocolBinding, bool) {
	if c == nil || channel == nil || expected == "" {
		return jsplugin.ProtocolBinding{}, false
	}
	value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint)
	pinned, ok := value.(jsplugin.PinnedEndpoint)
	if !exists || !ok || pinned.Generation == nil || pinned.Plugin == nil {
		return jsplugin.ProtocolBinding{}, false
	}
	candidates := pinned.Candidates
	if len(candidates) == 0 {
		candidates = []jsplugin.ProtocolBinding{{Plugin: pinned.Plugin, Protocol: pinned.Protocol, Operation: pinned.Operation, Model: pinned.Model}}
	}
	expectedOwned := false
	selected := jsplugin.ProtocolBinding{}
	setting := channel.GetSetting()
	for _, candidate := range candidates {
		if candidate.Plugin == nil {
			continue
		}
		if candidate.Plugin.Meta.Key == expected {
			expectedOwned = true
		}
		if channel.Type == constant.ChannelTypeTaskPlugin || channel.Type == constant.ChannelTypeNewAPI {
			if selected.Plugin == nil && setting.BindsTaskPlugin(candidate.Plugin.Meta.Key) {
				selected = candidate
			}
			continue
		}
		plugin, indexed := pinned.Generation.GetByChannelType(channel.Type)
		if indexed && plugin == candidate.Plugin {
			selected = candidate
		}
	}
	return selected, expectedOwned && selected.Plugin != nil
}

// SetupContextForSelectedChannelWithKeyExclusions initializes channel context while skipping failed keys from this request.
func SetupContextForSelectedChannelWithKeyExclusions(c *gin.Context, channel *model.Channel, modelName string, excludedFingerprints map[string]struct{}) *types.NewAPIError {
	return setupContextForSelectedChannel(c, channel, modelName, nil, excludedFingerprints)
}

// SetupContextForSelectedChannelKey initializes channel context with one exact multi-key index.
func SetupContextForSelectedChannelKey(c *gin.Context, channel *model.Channel, modelName string, keyIndex int) *types.NewAPIError {
	return setupContextForSelectedChannel(c, channel, modelName, &keyIndex, nil)
}

// setupContextForSelectedChannel shares channel metadata setup between normal routing and targeted key tests.
func setupContextForSelectedChannel(c *gin.Context, channel *model.Channel, modelName string, keyIndex *int, excludedFingerprints map[string]struct{}) *types.NewAPIError {
	if common.GetContextKeyString(c, constant.ContextKeyOriginalModel) == "" {
		common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
	}
	common.SetContextKey(c, constant.ContextKeyAttemptModel, modelName)
	if channel == nil {
		logTaskPluginChannelDecision(c, nil, modelName, "channel_rejected", "nil_channel")
		return types.NewError(errors.New("channel is nil"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	expectedPlugin := c.GetString("expected_task_plugin_key")
	if expectedPlugin != "" && !channelMatchesExpectedTaskPlugin(c, channel, expectedPlugin) {
		logTaskPluginChannelDecision(c, channel, modelName, "channel_rejected", "identity_mismatch")
		return types.NewError(errors.New("selected channel does not match the pinned task plugin"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if candidate, matched := pinnedEndpointCandidateForChannel(c, channel, expectedPlugin); matched {
		if value, exists := c.Get(jsplugin.ContextKeyPinnedEndpoint); exists {
			if pinned, ok := value.(jsplugin.PinnedEndpoint); ok && candidate.Plugin != nil && candidate.Plugin != pinned.Plugin {
				pinned.Plugin = candidate.Plugin
				pinned.Protocol = candidate.Protocol
				pinned.Operation = candidate.Operation
				c.Set(jsplugin.ContextKeyPinnedEndpoint, pinned)
				c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: pinned.Generation, Plugin: candidate.Plugin})
				c.Set("expected_task_plugin_key", candidate.Plugin.Meta.Key)
				c.Set("task_plugin_key", candidate.Plugin.Meta.Key)
				expectedPlugin = candidate.Plugin.Meta.Key
				logger.LogDebug(c, "task_plugin subsystem=endpoint event=provider_selected generation=%d plugin=%q model=%q channel_id=%d channel_type=%d", pinned.Generation.Number, candidate.Plugin.Meta.Key, modelName, channel.Id, channel.Type)
			}
		}
	}
	common.SetContextKey(c, constant.ContextKeyChannelSnapshot, channel.Snapshot())
	common.SetContextKey(c, constant.ContextKeyChannelRoutePlan, nil)
	requestPath := ""
	if c.Request != nil && c.Request.URL != nil {
		requestPath = c.Request.URL.Path
	}
	plan, err := service.PlanChannelProtocolRoute(channel, modelName, requestPath, common.GetContextKeyBool(c, constant.ContextKeyIsStream))
	if err != nil {
		return types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if _, _, _, isTextProtocol := service.ResolveClientTextProtocol(requestPath); isTextProtocol && plan == nil {
		return types.NewError(errors.New("channel has no compatible protocol route"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if plan != nil {
		common.SetContextKey(c, constant.ContextKeyChannelRoutePlan, *plan)
	}
	common.SetContextKey(c, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(c, constant.ContextKeyChannelName, channel.Name)
	common.SetContextKey(c, constant.ContextKeyChannelType, channel.Type)
	common.SetContextKey(c, constant.ContextKeyChannelCreateTime, channel.CreatedTime)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, channel.GetSetting())
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, channel.GetOtherSettings())
	switch channel.Type {
	case constant.ChannelTypeTaskPlugin:
		c.Set("task_plugin_key", channel.GetSetting().TaskPluginKey)
	case constant.ChannelTypeNewAPI:
		if expectedPlugin != "" {
			c.Set("task_plugin_key", expectedPlugin)
		}
	}
	logTaskPluginChannelDecision(c, channel, modelName, "channel_selected", "")
	paramOverride := channel.GetParamOverride()
	headerOverride := channel.GetHeaderOverride()
	if mergedParam, applied := service.ApplyChannelAffinityOverrideTemplate(c, paramOverride); applied {
		paramOverride = mergedParam
	}
	common.SetContextKey(c, constant.ContextKeyChannelParamOverride, paramOverride)
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, headerOverride)
	organization := ""
	if channel.OpenAIOrganization != nil {
		organization = *channel.OpenAIOrganization
	}
	common.SetContextKey(c, constant.ContextKeyChannelOrganization, organization)
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, channel.GetAutoBan())
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, channel.GetModelMapping())
	common.SetContextKey(c, constant.ContextKeyChannelStatusCodeMapping, channel.GetStatusCodeMapping())

	mappingModel := modelName
	if strings.HasSuffix(requestPath, "/responses/compact") {
		mappingModel = strings.TrimSuffix(mappingModel, ratio_setting.CompactModelSuffix)
	}
	healthModel, mappingErr := common.ResolveMappedModel(channel.GetModelMapping(), mappingModel)
	if mappingErr != nil {
		return types.NewError(mappingErr, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	common.SetContextKey(c, constant.ContextKeyChannelHealthModel, healthModel)
	key := ""
	index := 0
	if keyIndex == nil {

		selectedKey, selectedIndex, newAPIError := service.SelectChannelKeyForRequest(c, channel, healthModel, excludedFingerprints)
		if newAPIError != nil {
			return newAPIError
		}
		key = selectedKey
		index = selectedIndex
	} else {
		if !channel.ChannelInfo.IsMultiKey {
			return types.NewError(errors.New("key_index is only supported for multi-key channels"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		keys := channel.GetKeys()
		if *keyIndex < 0 || *keyIndex >= len(keys) {
			return types.NewError(fmt.Errorf("key_index %d is out of range", *keyIndex), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		key = keys[*keyIndex]
		index = *keyIndex
	}
	if channel.ChannelInfo.IsMultiKey {
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
		common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
	} else {
		// 必须设置为 false，否则在重试到单个 key 的时候会导致日志显示错误
		common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, false)
	}
	// c.Request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	common.SetContextKey(c, constant.ContextKeyChannelKey, key)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, channel.GetBaseURL())

	common.SetContextKey(c, constant.ContextKeySystemPromptOverride, false)
	common.SetContextKey(c, constant.ContextKeySystemPromptApplied, false)
	common.SetContextKey(c, constant.ContextKeySystemPromptSource, "")
	common.SetContextKey(c, constant.ContextKeySystemPromptModel, "")
	common.SetContextKey(c, constant.ContextKeySystemPromptTokens, 0)

	// 切换渠道前清理上一渠道的类型专属参数，避免跨类型重试串值。
	c.Set("api_version", "")
	c.Set("region", "")
	c.Set("plugin", "")
	c.Set("bot_id", "")
	// TODO: api_version统一
	switch channel.Type {
	case constant.ChannelTypeAzure:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeVertexAi:
		c.Set("region", channel.Other)
	case constant.ChannelTypeXunfei:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeGemini:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeAli:
		c.Set("plugin", channel.Other)
	case constant.ChannelCloudflare:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeMokaAI:
		c.Set("api_version", channel.Other)
	case constant.ChannelTypeCoze:
		c.Set("bot_id", channel.Other)
	}
	return nil
}

// extractModelNameFromGeminiPath 从 Gemini API URL 路径中提取模型名
// 输入格式: /v1beta/models/gemini-2.0-flash:generateContent
// 输出: gemini-2.0-flash
func extractModelNameFromGeminiPath(path string) string {
	// 查找 "/models/" 的位置
	modelsPrefix := "/models/"
	modelsIndex := strings.Index(path, modelsPrefix)
	if modelsIndex == -1 {
		return ""
	}

	// 从 "/models/" 之后开始提取
	startIndex := modelsIndex + len(modelsPrefix)
	if startIndex >= len(path) {
		return ""
	}

	// 查找 ":" 的位置，模型名在 ":" 之前
	colonIndex := strings.Index(path[startIndex:], ":")
	if colonIndex == -1 {
		// 如果没有找到 ":"，返回从 "/models/" 到路径结尾的部分
		return path[startIndex:]
	}

	// 返回模型名部分
	return path[startIndex : startIndex+colonIndex]
}
