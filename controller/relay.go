package controller

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func relayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		err = relay.ImageHelper(c, info)
	case relayconstant.RelayModeAudioSpeech:
		fallthrough
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err = relay.AudioHelper(c, info)
	case relayconstant.RelayModeRerank:
		err = relay.RerankHelper(c, info)
	case relayconstant.RelayModeEmbeddings:
		err = relay.EmbeddingHelper(c, info)
	case relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact:
		err = relay.ResponsesHelper(c, info)
	default:
		err = relay.TextHelper(c, info)
	}
	return err
}

func geminiRelayHandler(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	var err *types.NewAPIError
	if strings.Contains(c.Request.URL.Path, "embed") {
		err = relay.GeminiEmbeddingHandler(c, info)
	} else {
		err = relay.GeminiHelper(c, info)
	}
	return err
}

func Relay(c *gin.Context, relayFormat types.RelayFormat) {
	handlerStartedAt := time.Now()

	requestId := c.GetString(common.RequestIdKey)
	//group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	//originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	var (
		newAPIError           *types.NewAPIError
		clientResponseError   *types.NewAPIError
		rawFinalError         *types.NewAPIError
		ws                    *websocket.Conn
		relayInfo             *relaycommon.RelayInfo
		relayStarted          bool
		billingFinalizerArmed bool
	)

	if relayFormat == types.RelayFormatOpenAIRealtime {
		var err error
		ws, err = upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			helper.WssError(c, ws, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry()).ToOpenAIError())
			return
		}
		defer ws.Close()
	}

	defer func() {
		if relayInfo != nil {
			relayInfo.StopStreamPinger()
		}
		if newAPIError != nil && billingFinalizerArmed {
			newAPIError = service.NormalizeViolationFeeError(newAPIError)
		}
		if newAPIError != nil {
			if c.Request.Context().Err() != nil {
				logger.LogInfo(c, "relay canceled by client")
			} else {
				logger.LogError(c, fmt.Sprintf("relay error: %s", common.LocalLogPreview(newAPIError.Error())))
				newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), requestId))
				if relayStarted {
					recordRelayErrorLog(c, relayInfo, newAPIError, relayClientErrorLogContent(newAPIError, relayFormat), rawFinalError, false)
				}
				writeRelayErrorResponse(c, relayFormat, ws, relayInfo, newAPIError)
			}
		} else if clientResponseError != nil {
			clientResponseError.SetMessage(common.MessageWithRequestId(clientResponseError.Error(), requestId))
			writeRelayErrorResponse(c, relayFormat, ws, relayInfo, clientResponseError)
		}
		if newAPIError != nil && billingFinalizerArmed {
			// 错误帧写入后再结算，使同一条消费日志记录最终客户端终止状态。
			if _, finalizeErr := service.FinalizeTextBilling(c, relayInfo, nil, newAPIError); finalizeErr != nil {
				logger.LogError(c, "error finalizing failed stream billing: "+finalizeErr.Error())
			}
			service.ChargeViolationFeeIfNeeded(c, relayInfo, newAPIError)
		}
		conversationError := newAPIError
		if conversationError == nil {
			conversationError = clientResponseError
		}
		service.RecordConversationLog(c, relayInfo, conversationError)
	}()

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	requestReadFinishedAt := time.Now()
	if err != nil {
		// Map "request body too large" to 413 so clients can handle it correctly
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest)
		}
		return
	}

	relayInfo, err = relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}
	relayInfo.OriginHandlerStartedAt = handlerStartedAt
	relayInfo.RequestUploadDuration = requestReadFinishedAt.Sub(handlerStartedAt)
	if c.Request.ContentLength > 0 {
		relayInfo.IncomingRequestBodyBytes = c.Request.ContentLength
	}
	if modalityErr := helper.ValidateRequestInputModalities(c, relayInfo.GetRequestedModelName(), request); modalityErr != nil {
		newAPIError = modalityErr
		return
	}

	needSensitiveCheck := setting.ShouldCheckPromptSensitive()
	needCountToken := constant.CountToken
	// Avoid building huge CombineText (strings.Join) when token counting and sensitive check are both disabled.
	var meta *types.TokenCountMeta
	if needSensitiveCheck || needCountToken {
		meta = request.GetTokenCountMeta()
	} else {
		meta = fastTokenCountMetaForPricing(request)
	}

	if needSensitiveCheck && meta != nil {
		contains, words := service.CheckSensitiveText(meta.CombineText)
		if contains {
			logger.LogWarn(c, fmt.Sprintf("user sensitive words detected: %s", strings.Join(words, ", ")))
			newAPIError = types.NewError(err, types.ErrorCodeSensitiveWordsDetected)
			return
		}
	}

	tokens, err := service.EstimateRequestToken(c, meta, relayInfo)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeCountTokenFailed)
		return
	}

	relayInfo.SetEstimatePromptTokens(tokens)
	meta, newAPIError = prepareContextFallback(c, relayInfo, request, tokens)
	if newAPIError != nil {
		return
	}
	if relayInfo.IsContextFallbackActive() {
		installContextFallbackResponseWriter(c, relayInfo.GetRequestedModelName())
	}
	tokens = relayInfo.GetEstimatePromptTokens()

	priceData, err := helper.ModelPriceHelper(c, relayInfo, tokens, meta)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest))
		return
	}

	// common.SetContextKey(c, constant.ContextKeyTokenCountMeta, meta)

	if priceData.FreeModel {
		logger.LogInfo(c, fmt.Sprintf("模型 %s 免费，跳过预扣费", relayInfo.OriginModelName))
	} else {
		newAPIError = service.PreConsumeBilling(c, priceData.QuotaToPreConsume, relayInfo)
		if newAPIError != nil {
			return
		}
	}

	// 失败结算由最外层 defer 在协议错误帧写入后统一执行。
	billingFinalizerArmed = true

	retryGroup := relayRetryGroup(relayInfo)
	if relayInfo.IsContextFallbackActive() {
		retryGroup = contextFallbackRoutingGroup(c, relayInfo)
	}
	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  retryGroup,
		ModelName:   relayInfo.GetAttemptModelName(),
		RequestPath: c.Request.URL.Path,
		RelayFormat: relayInfo.RelayFormat,
		IsStream:    relayInfo.IsStream,
		Retry:       common.GetPointer(0),
	}
	if relayInfo.IsContextFallbackActive() && relayInfo.ContextFallback.RouteMode == dto.ContextFallbackModeCross {
		retryParam.ExcludedChannelIDs = map[int]struct{}{relayInfo.ContextFallback.SourceChannelID: {}}
	}
	relayInfo.RetryIndex = 0
	relayInfo.LastError = nil
	relayStarted = true

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		relayInfo.RetryIndex = retryParam.GetRetry()
		channel, channelErr := getChannel(c, relayInfo, retryParam)
		if channelErr != nil {
			logger.LogError(c, channelErr.Error())
			newAPIError = channelErr
			break
		}

		addUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			// Ensure consistent 413 for oversized bodies even when error occurs later (e.g., retry path)
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
			} else {
				newAPIError = types.NewErrorWithStatusCode(bodyErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		switch relayFormat {
		case types.RelayFormatOpenAIRealtime:
			newAPIError = relay.WssHelper(c, relayInfo)
		case types.RelayFormatClaude:
			newAPIError = relay.ClaudeHelper(c, relayInfo)
		case types.RelayFormatGemini:
			newAPIError = geminiRelayHandler(c, relayInfo)
		default:
			newAPIError = relayHandler(c, relayInfo)
		}

		if newAPIError == nil {
			relayInfo.LastError = nil
			// Response overrides may map one successful, billable upstream attempt
			// to a client 4xx/5xx without changing its internal success semantics.
			common.SetContextKey(c, constant.ContextKeyRelayUpstreamSucceeded, true)
			clientResponseError = finalizeResponseOverride(c, relayInfo)
			return
		}
		if buffer := relaycommon.CurrentResponseOverrideBuffer(c); buffer != nil {
			buffer.MarkRelayError()
			buffer.Discard(c)
		}
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			if relayInfo.StreamStatus != nil {
				relayInfo.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, requestErr)
			}
			return
		}

		newAPIError = service.NormalizeViolationFeeError(newAPIError)
		relayInfo.LastError = newAPIError

		willRetry := shouldRetry(c, newAPIError, common.RetryTimes-retryParam.GetRetry())
		if relayInfo.IsContextFallbackActive() && relayInfo.ContextFallback.RouteMode == dto.ContextFallbackModeSame {
			willRetry = false
		}
		endpointMismatch := relayInfo.ProtocolEndpointMismatch &&
			relayInfo.ChannelRoutePlan != nil &&
			relayInfo.ChannelRoutePlan.RouteMode != types.ChannelRouteModeLegacy
		if endpointMismatch {
			if retryParam.ExcludedChannelIDs == nil {
				retryParam.ExcludedChannelIDs = make(map[int]struct{})
			}
			retryParam.ExcludedChannelIDs[channel.Id] = struct{}{}
			willRetry = common.RetryTimes-retryParam.GetRetry() > 0 &&
				!(relayInfo.IsContextFallbackActive() && relayInfo.ContextFallback.RouteMode == dto.ContextFallbackModeSame)
		} else {
			processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)
		}
		if relayInfo.IsStream {
			clientCommitted := c.Writer.Written()
			if relayInfo.StreamStatus != nil &&
				relayInfo.StreamStatus.StreamPolicyVersion() == "progressive-v1" {
				// 渐进策略仅在业务 payload 提交后停止透明重试；HTTP 头和 APP Ping 不消耗重试资格。
				clientCommitted = relayInfo.StreamStatus.ClientPayloadIsCommitted()
			}
			if clientCommitted {
				willRetry = false
			}
		}
		if willRetry {
			recordRelayErrorLog(c, relayInfo, newAPIError, "", nil, true)
		}
		if !willRetry {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}
	if newAPIError != nil {
		gopool.Go(func() {
			perfmetrics.RecordRelaySample(relayInfo, false, 0)
		})
		rawFinalError = newAPIError
		if relayInfo.LastError == nil {
			relayInfo.LastError = newAPIError
		}
		if !types.IsClientErrorWritten(newAPIError) {
			newAPIError = resolveConfiguredFinalRelayError(c, relayInfo)
		}
	}
}

// finalizeResponseOverride commits the successful upstream response or replaces
// it with the independently configured client response.
func finalizeResponseOverride(c *gin.Context, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	buffer := relaycommon.CurrentResponseOverrideBuffer(c)
	if buffer == nil {
		return nil
	}
	if relayInfo != nil && relayInfo.ResponseOverride != nil && relayInfo.ResponseOverride.Applied {
		buffer.Discard(c)
		return relayInfo.ResponseOverride.ClientError
	}
	if err := buffer.Commit(c); err != nil {
		logger.LogError(c, fmt.Sprintf("failed to commit buffered relay response: %s", err.Error()))
	}
	return nil
}

// writeRelayErrorResponse 按客户端协议输出错误，已提交的流式响应继续使用 SSE 帧。
func writeRelayErrorResponse(c *gin.Context, relayFormat types.RelayFormat, ws *websocket.Conn, relayInfo *relaycommon.RelayInfo, newAPIError *types.NewAPIError) {
	if newAPIError == nil || types.IsClientErrorWritten(newAPIError) {
		return
	}
	if relayFormat == types.RelayFormatOpenAIRealtime {
		helper.WssError(c, ws, relayClientOpenAIError(newAPIError))
		return
	}

	streamCommitted := relayInfo != nil && relayInfo.IsStream && c.Writer.Written()
	if streamCommitted {
		// 客户端已断开时无需继续写错误帧，也避免产生无意义的写失败日志。
		if c.Request.Context().Err() != nil {
			return
		}
		if relayInfo.StreamStatus != nil && !relayInfo.StreamStatus.TryMarkErrorFrameWritten() {
			return
		}
		if relayInfo.StreamStatus != nil {
			relayInfo.StreamStatus.SetTerminal("error", "failed")
			relayInfo.StreamStatus.SetEndReason(relaycommon.StreamEndReasonHandlerStop, newAPIError)
		}
		if relayFormat == types.RelayFormatClaude {
			_ = helper.ClaudeData(c, dto.ClaudeResponse{
				Type:  "error",
				Error: relayClientClaudeError(newAPIError),
			})
			return
		}
		if relayFormat == types.RelayFormatOpenAIResponses {
			openAIError := relayClientOpenAIError(newAPIError)
			streamError := dto.ResponsesStreamResponse{
				Type:    "error",
				Code:    openAIError.Code,
				Message: openAIError.Message,
				Param:   openAIError.Param,
			}
			data, err := common.Marshal(streamError)
			if err != nil {
				logger.LogError(c, "marshal responses stream error failed: "+err.Error())
				return
			}
			if err = helper.ResponseChunkData(c, streamError, string(data)); err != nil {
				logger.LogError(c, "write responses stream error failed: "+err.Error())
			}
			return
		}
		if err := helper.ObjectData(c, gin.H{"error": relayClientOpenAIError(newAPIError)}); err != nil {
			logger.LogError(c, "write stream error failed: "+err.Error())
		}
		return
	}

	if relayFormat == types.RelayFormatClaude {
		prepareRelayErrorHeaders(c)
		c.JSON(newAPIError.StatusCode, gin.H{
			"type":  "error",
			"error": relayClientClaudeError(newAPIError),
		})
		return
	}
	if relayFormat == types.RelayFormatGemini {
		prepareRelayErrorHeaders(c)
		c.JSON(newAPIError.StatusCode, dto.GeminiErrorResponse{
			Error: dto.GeminiError{
				Code:    newAPIError.StatusCode,
				Message: relayClientOpenAIError(newAPIError).Message,
				Status:  geminiCanonicalErrorStatus(newAPIError.StatusCode),
			},
		})
		return
	}
	prepareRelayErrorHeaders(c)
	c.JSON(newAPIError.StatusCode, gin.H{
		"error": relayClientOpenAIError(newAPIError),
	})
}

// relayClientOpenAIError 使用当前错误消息生成 OpenAI 协议错误，保留请求 ID 等网关补充信息。
func relayClientOpenAIError(newAPIError *types.NewAPIError) types.OpenAIError {
	openAIError := newAPIError.ToOpenAIError()
	openAIError.Message = newAPIError.MaskSensitiveError()
	return openAIError
}

// relayClientClaudeError 使用当前错误消息生成 Claude 协议错误，保留请求 ID 等网关补充信息。
func relayClientClaudeError(newAPIError *types.NewAPIError) types.ClaudeError {
	claudeError := newAPIError.ToClaudeError()
	claudeError.Message = newAPIError.MaskSensitiveError()
	return claudeError
}

// geminiCanonicalErrorStatus maps HTTP status codes to Google RPC canonical status names.
func geminiCanonicalErrorStatus(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound, http.StatusGone:
		return "NOT_FOUND"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusConflict:
		return "ABORTED"
	case http.StatusPreconditionFailed:
		return "FAILED_PRECONDITION"
	case http.StatusRequestEntityTooLarge, http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusRequestedRangeNotSatisfiable:
		return "OUT_OF_RANGE"
	case 499:
		return "CANCELLED"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	default:
		if statusCode >= http.StatusInternalServerError {
			return "INTERNAL"
		}
		return "UNKNOWN"
	}
}

// prepareRelayErrorHeaders removes representation-specific headers inherited from a discarded upstream body.
func prepareRelayErrorHeaders(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	for _, key := range []string{"Content-Length", "Content-Encoding", "ETag", "Content-Range", "Transfer-Encoding"} {
		c.Writer.Header().Del(key)
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
}

// CountTokens 返回 Claude Messages 请求的本地输入 token 估算，不进入上游和计费链。
func CountTokens(c *gin.Context) {
	request, err := helper.GetAndValidateRequest(c, types.RelayFormatClaude)
	if err != nil {
		newAPIError := types.NewErrorWithStatusCode(
			err,
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
		writeCountTokensErrorResponse(c, newAPIError)
		return
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatClaude, request, nil)
	if err != nil {
		newAPIError := types.NewError(err, types.ErrorCodeGenRelayInfoFailed, types.ErrOptionWithSkipRetry())
		writeCountTokensErrorResponse(c, newAPIError)
		return
	}
	if modalityErr := helper.ValidateRequestInputModalities(c, relayInfo.GetRequestedModelName(), request); modalityErr != nil {
		writeCountTokensErrorResponse(c, modalityErr)
		return
	}

	tokens, err := service.CountRequestToken(c, request.GetTokenCountMeta(), relayInfo)
	if err != nil {
		newAPIError := types.NewError(err, types.ErrorCodeCountTokenFailed, types.ErrOptionWithSkipRetry())
		writeCountTokensErrorResponse(c, newAPIError)
		return
	}
	c.JSON(http.StatusOK, gin.H{"input_tokens": tokens})
}

// writeCountTokensErrorResponse 复用 Messages 协议错误出口并附加请求 ID。
func writeCountTokensErrorResponse(c *gin.Context, newAPIError *types.NewAPIError) {
	if newAPIError == nil {
		return
	}
	newAPIError.SetMessage(common.MessageWithRequestId(newAPIError.Error(), c.GetString(common.RequestIdKey)))
	writeRelayErrorResponse(c, types.RelayFormatClaude, nil, nil, newAPIError)
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol TODO add other protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

func fastTokenCountMetaForPricing(request dto.Request) *types.TokenCountMeta {
	if request == nil {
		return &types.TokenCountMeta{}
	}
	meta := &types.TokenCountMeta{
		TokenType: types.TokenTypeTokenizer,
	}
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		maxCompletionTokens := lo.FromPtrOr(r.MaxCompletionTokens, uint(0))
		maxTokens := lo.FromPtrOr(r.MaxTokens, uint(0))
		if maxCompletionTokens > maxTokens {
			meta.MaxTokens = int(maxCompletionTokens)
		} else {
			meta.MaxTokens = int(maxTokens)
		}
	case *dto.OpenAIResponsesRequest:
		meta.MaxTokens = int(lo.FromPtrOr(r.MaxOutputTokens, uint(0)))
	case *dto.ClaudeRequest:
		meta.MaxTokens = int(lo.FromPtr(r.MaxTokens))
	case *dto.ImageRequest:
		// Pricing for image requests depends on ImagePriceRatio; safe to compute even when CountToken is disabled.
		return r.GetTokenCountMeta()
	default:
		// Best-effort: leave CombineText empty to avoid large allocations.
	}
	return meta
}

func getChannel(c *gin.Context, info *relaycommon.RelayInfo, retryParam *service.RetryParam) (*model.Channel, *types.NewAPIError) {
	if info.ChannelMeta == nil {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		return &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}, nil
	}
	var channel *model.Channel
	var selectGroup string
	var err error
	if info.IsContextFallbackActive() && info.ContextFallback.RouteMode == dto.ContextFallbackModeSame {
		selectGroup = contextFallbackRoutingGroup(c, info)
		channel, err = model.CacheGetChannel(info.ContextFallback.SourceChannelID)
		if err == nil && !contextFallbackTargetEligible(channel, selectGroup, info.GetAttemptModelName(), c.Request.URL.Path, info.IsStream) {
			channel = nil
		}
	} else if info.IsContextFallbackActive() && info.ContextFallback.RouteMode == dto.ContextFallbackModeCross {
		var channelErr *types.NewAPIError
		channel, channelErr = selectContextFallbackTarget(c, info, retryParam)
		if channelErr != nil {
			return nil, channelErr
		}
		selectGroup = contextFallbackRoutingGroup(c, info)
	} else {
		retryParam.IsStream = info.IsStream
		retryParam.RelayFormat = info.RelayFormat
		channel, _, selectGroup, err = service.CacheGetRandomSatisfiedChannelWithRoute(retryParam)
		info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)
	}

	if err != nil {
		return nil, types.NewError(fmt.Errorf("获取分组 %s 下模型 %s 的可用渠道失败（retry）: %s", selectGroup, info.OriginModelName, err.Error()), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		return nil, types.NewError(fmt.Errorf("分组 %s 下模型 %s 的可用渠道不存在（retry）", selectGroup, info.OriginModelName), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	common.SetContextKey(c, constant.ContextKeyIsStream, info.IsStream)
	newAPIError := middleware.SetupContextForSelectedChannel(c, channel, info.GetAttemptModelName())
	if newAPIError != nil {
		return nil, newAPIError
	}
	if modalityErr := helper.ValidateRequestInputModalities(c, info.GetRequestedModelName(), info.Request); modalityErr != nil {
		return nil, modalityErr
	}
	info.ProtocolEndpointMismatch = false
	if routePlan, ok := common.GetContextKeyType[types.ChannelRoutePlan](c, constant.ContextKeyChannelRoutePlan); ok {
		info.ChannelRoutePlan = &routePlan
		info.RequestConversionChain = []types.RelayFormat{routePlan.ClientRelayFormat}
		info.FinalRequestRelayFormat = ""
	} else {
		info.ChannelRoutePlan = nil
		info.RequestConversionChain = nil
		info.InitRequestConversionChain()
	}
	if info.IsContextFallbackActive() {
		info.ContextFallback.TargetChannelID = channel.Id
		if _, previewErr := prepareContextFallbackTarget(c, info, info.Request); previewErr != nil {
			return nil, previewErr
		}
	}
	return channel, nil
}

// relayRetryGroup keeps retries inside the group whose subscription funds the request.
func relayRetryGroup(info *relaycommon.RelayInfo) string {
	if info != nil && info.SubscriptionEntitlementGroup != "" {
		return info.SubscriptionEntitlementGroup
	}
	if info == nil {
		return ""
	}
	return info.TokenGroup
}

func shouldRetry(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	if c != nil && c.Writer != nil && c.Writer.Written() {
		return false
	}
	if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	if types.IsChannelError(openaiErr) {
		return true
	}
	if types.IsSkipRetryError(openaiErr) {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	code := openaiErr.StatusCode
	if code >= 200 && code < 300 {
		return false
	}
	if code < 100 || code > 599 {
		return true
	}
	if operation_setting.IsAlwaysSkipRetryCode(openaiErr.GetErrorCode()) {
		return false
	}
	return operation_setting.ShouldRetryByStatusCode(code)
}

// resolveConfiguredFinalRelayError applies channel and system final_error rules after retries finish.
func resolveConfiguredFinalRelayError(c *gin.Context, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	if relayInfo != nil && relayInfo.LastError != nil &&
		relayInfo.LastError.GetErrorCode() == types.ErrorCodeUnsupportedInputModality {
		return relayInfo.LastError
	}
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		mapped, matched, err := relaycommon.ApplyFinalErrorOverride(relayInfo.ChannelMeta.ParamOverride, relayInfo)
		if err != nil {
			logger.LogError(c, fmt.Sprintf("invalid channel final_error override: %s", err.Error()))
		} else if matched {
			return mapped
		}
	}

	mapped, matched, err := relaycommon.ApplyFinalErrorOverride(operation_setting.GetDefaultFinalErrorOverride(), relayInfo)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("invalid default final_error override: %s", err.Error()))
	} else if matched {
		return mapped
	}

	return types.NewOpenAIError(
		errors.New(http.StatusText(http.StatusBadGateway)),
		types.ErrorCodeBadResponse,
		http.StatusBadGateway,
		types.ErrOptionWithSkipRetry(),
	)
}

// relayClientErrorLogContent returns the error text sent to the client.
func relayClientErrorLogContent(err *types.NewAPIError, relayFormat types.RelayFormat) string {
	if err == nil {
		return ""
	}
	message := err.ToOpenAIError().Message
	if relayFormat == types.RelayFormatClaude {
		message = err.ToClaudeError().Message
	}
	if err.StatusCode == 0 {
		return message
	}
	if message == "" {
		return fmt.Sprintf("status_code=%d", err.StatusCode)
	}
	return fmt.Sprintf("status_code=%d, %s", err.StatusCode, message)
}

// recordRelayErrorLog writes one relay error using the current channel context.
func recordRelayErrorLog(c *gin.Context, relayInfo *relaycommon.RelayInfo, err *types.NewAPIError, content string, rawError *types.NewAPIError, isIntermediate bool) {
	if !constant.ErrorLogEnabled || !types.IsRecordErrorLog(err) {
		return
	}
	if content == "" {
		content = err.MaskSensitiveErrorWithStatusCode()
	}
	userId := c.GetInt("id")
	tokenName := c.GetString("token_name")
	modelName := c.GetString("original_model")
	tokenId := c.GetInt("token_id")
	userGroup := c.GetString("group")
	channelId := c.GetInt("channel_id")
	if relayInfo != nil && relayInfo.IsContextFallbackActive() {
		channelId = relayInfo.ContextFallback.SourceChannelID
	}
	other := make(map[string]interface{})
	if c.Request != nil && c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
	}
	other["error_type"] = err.GetErrorType()
	other["error_code"] = err.GetErrorCode()
	other["status_code"] = err.StatusCode
	other["channel_id"] = channelId
	if relayInfo == nil || !relayInfo.IsContextFallbackActive() {
		other["channel_name"] = c.GetString("channel_name")
		other["channel_type"] = c.GetInt("channel_type")
	}
	if rawError != nil {
		other["public_error"] = true
	}
	adminInfo := make(map[string]interface{})
	adminInfo["use_channel"] = c.GetStringSlice("use_channel")
	isMultiKey := common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey)
	if isMultiKey {
		adminInfo["is_multi_key"] = true
		adminInfo["multi_key_index"] = common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex)
	}
	service.AppendChannelAffinityAdminInfo(c, adminInfo)
	service.AppendContextFallbackAdminInfo(relayInfo, adminInfo)
	service.AppendResponseOverrideAdminInfo(relayInfo, adminInfo)
	service.AppendStreamStatus(relayInfo, other)
	if rawError != nil {
		adminInfo["upstream_error"] = common.LocalLogPreview(rawError.MaskSensitiveErrorWithStatusCode())
	}
	other["admin_info"] = adminInfo
	startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if startTime.IsZero() {
		startTime = time.Now()
	}
	useTimeSeconds := int(time.Since(startTime).Seconds())
	model.RecordErrorLog(c, userId, channelId, modelName, tokenName, content, tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), isIntermediate, userGroup, other)
}

// processChannelError applies channel health handling for a failed attempt.
func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError) {
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.Error())))
	// 不要使用context获取渠道信息，异步处理时可能会出现渠道信息不一致的情况
	// do not use context to get channel info, there may be inconsistent channel info when processing asynchronously
	if service.ShouldDisableChannel(err) && channelError.AutoBan {
		gopool.Go(func() {
			service.DisableChannel(channelError, err.ErrorWithStatusCode())
		})
	}
}

func RelayMidjourney(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatMjProxy, nil, nil)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"description": fmt.Sprintf("failed to generate relay info: %s", err.Error()),
			"type":        "upstream_error",
			"code":        4,
		})
		return
	}

	var mjErr *dto.MidjourneyResponse
	switch relayInfo.RelayMode {
	case relayconstant.RelayModeMidjourneyNotify:
		mjErr = relay.RelayMidjourneyNotify(c)
	case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition:
		mjErr = relay.RelayMidjourneyTask(c, relayInfo.RelayMode)
	case relayconstant.RelayModeMidjourneyTaskImageSeed:
		mjErr = relay.RelayMidjourneyTaskImageSeed(c)
	case relayconstant.RelayModeSwapFace:
		mjErr = relay.RelaySwapFace(c, relayInfo)
	default:
		mjErr = relay.RelayMidjourneySubmit(c, relayInfo)
	}
	//err = relayMidjourneySubmit(c, relayMode)
	log.Println(mjErr)
	if mjErr != nil {
		statusCode := http.StatusBadRequest
		if mjErr.Code == 30 {
			mjErr.Result = "当前分组负载已饱和，请稍后再试，或升级账户以提升服务质量。"
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result),
			"type":        "upstream_error",
			"code":        mjErr.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.LogError(c, fmt.Sprintf("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result)))
	}
}

func RelayNotImplemented(c *gin.Context) {
	err := types.OpenAIError{
		Message: "API not implemented",
		Type:    "new_api_error",
		Param:   "",
		Code:    "api_not_implemented",
	}
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": err,
	})
}

func RelayNotFound(c *gin.Context) {
	err := types.OpenAIError{
		Message: fmt.Sprintf("Invalid URL (%s %s)", c.Request.Method, c.Request.URL.Path),
		Type:    "invalid_request_error",
		Param:   "",
		Code:    "",
	}
	c.JSON(http.StatusNotFound, gin.H{
		"error": err,
	})
}

func RelayTaskFetch(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &dto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if taskErr := relay.RelayTaskFetch(c, relayInfo.RelayMode); taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

func RelayTask(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &dto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}

	if taskErr := relay.ResolveOriginTask(c, relayInfo); taskErr != nil {
		respondTaskError(c, taskErr)
		return
	}

	var result *relay.TaskSubmitResult
	var taskErr *dto.TaskError
	defer func() {
		if taskErr != nil && relayInfo.Billing != nil {
			relayInfo.Billing.Refund(c)
		}
	}()

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayRetryGroup(relayInfo),
		ModelName:   relayInfo.GetRoutingModelName(),
		RequestPath: c.Request.URL.Path,
		RelayFormat: relayInfo.RelayFormat,
		IsStream:    relayInfo.IsStream,
		Retry:       common.GetPointer(0),
	}

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		var channel *model.Channel

		if lockedCh, ok := relayInfo.LockedChannel.(*model.Channel); ok && lockedCh != nil {
			channel = lockedCh
			if retryParam.GetRetry() > 0 {
				if setupErr := middleware.SetupContextForSelectedChannel(c, channel, relayInfo.GetRoutingModelName()); setupErr != nil {
					taskErr = service.TaskErrorWrapperLocal(setupErr.Err, "setup_locked_channel_failed", http.StatusInternalServerError)
					break
				}
			}
		} else {
			var channelErr *types.NewAPIError
			channel, channelErr = getChannel(c, relayInfo, retryParam)
			if channelErr != nil {
				logger.LogError(c, channelErr.Error())
				taskErr = service.TaskErrorWrapperLocal(channelErr.Err, "get_channel_failed", http.StatusInternalServerError)
				break
			}
		}

		addUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusRequestEntityTooLarge)
			} else {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusBadRequest)
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		result, taskErr = relay.RelayTaskSubmit(c, relayInfo)
		if taskErr == nil {
			break
		}

		willRetry := shouldRetryTaskRelay(c, channel.Id, taskErr, common.RetryTimes-retryParam.GetRetry())
		if !taskErr.LocalError {
			relayError := types.NewOpenAIError(taskErr.Error, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode)
			processChannelError(c,
				*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey,
					common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()),
				relayError)
			recordRelayErrorLog(c, relayInfo, relayError, "", nil, willRetry)
		}

		if !willRetry {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := fmt.Sprintf("重试：%s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}

	// ── 成功：结算 + 日志 + 插入任务 ──
	if taskErr == nil {
		if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
			common.SysError("settle task billing error: " + settleErr.Error())
		}
		service.LogTaskConsumption(c, relayInfo)

		task := model.InitTask(result.Platform, relayInfo)
		task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
		task.PrivateData.BillingSource = relayInfo.BillingSource
		task.PrivateData.SubscriptionId = relayInfo.SubscriptionId
		task.PrivateData.TokenId = relayInfo.TokenId
		task.PrivateData.NodeName = common.NodeName
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			ModelPrice:      relayInfo.PriceData.ModelPrice,
			GroupRatio:      relayInfo.PriceData.GroupRatioInfo.GroupRatio,
			ModelRatio:      relayInfo.PriceData.ModelRatio,
			OtherRatios:     relayInfo.PriceData.OtherRatios(),
			OriginModelName: relayInfo.OriginModelName,
			PerCallBilling:  common.StringsContains(constant.TaskPricePatches, relayInfo.OriginModelName) || relayInfo.PriceData.UsePrice,
		}
		task.Quota = result.Quota
		task.Data = result.TaskData
		task.Action = relayInfo.Action
		if insertErr := task.Insert(); insertErr != nil {
			common.SysError("insert task error: " + insertErr.Error())
		}
	}

	if taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

// respondTaskError 统一输出 Task 错误响应（含 429 限流提示改写）
func respondTaskError(c *gin.Context, taskErr *dto.TaskError) {
	if taskErr.StatusCode == http.StatusTooManyRequests {
		taskErr.Message = "当前分组上游负载已饱和，请稍后再试"
	}
	c.JSON(taskErr.StatusCode, taskErr)
}

func shouldRetryTaskRelay(c *gin.Context, channelId int, taskErr *dto.TaskError, retryTimes int) bool {
	if taskErr == nil {
		return false
	}
	if service.ShouldSkipRetryAfterChannelAffinityFailure(c) {
		return false
	}
	if retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	if taskErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if taskErr.StatusCode == 307 {
		return true
	}
	if taskErr.StatusCode/100 == 5 {
		// 超时不重试
		if operation_setting.IsAlwaysSkipRetryStatusCode(taskErr.StatusCode) {
			return false
		}
		return true
	}
	if taskErr.StatusCode == http.StatusBadRequest {
		return false
	}
	if taskErr.StatusCode == 408 {
		// azure处理超时不重试
		return false
	}
	if taskErr.LocalError {
		return false
	}
	if taskErr.StatusCode/100 == 2 {
		return false
	}
	return true
}
