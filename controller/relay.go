package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

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
	case relayconstant.RelayModeAlphaSearch:
		err = relay.AlphaSearchHelper(c, info)
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

// Relay executes a normal client request through the shared relay pipeline.
func Relay(c *gin.Context, relayFormat types.RelayFormat) {
	executeRelayRequest(c, relayFormat, nil)
}

// executeRelayRequest keeps channel tests on the client pipeline while pinning their channel snapshot.
func executeRelayRequest(c *gin.Context, relayFormat types.RelayFormat, testChannel *model.Channel) (relayInfo *relaycommon.RelayInfo, newAPIError *types.NewAPIError) {
	defer service.FinishChannelKeyProbe(c, false)
	handlerStartedAt := time.Now()
	common.SetContextKey(c, constant.ContextKeyRelayUpstreamSucceeded, false)

	requestId := c.GetString(common.RequestIdKey)
	//group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	//originalModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)

	var (
		clientResponseError   *types.NewAPIError
		rawFinalError         *types.NewAPIError
		ws                    *websocket.Conn
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
		finalization := relaycommon.BillingFinalization("")
		if newAPIError != nil && billingFinalizerArmed {
			// 错误帧写入后再结算，使同一条消费日志记录最终客户端终止状态。
			var finalizeErr error
			finalization, finalizeErr = service.FinalizeTextBilling(c, relayInfo, nil, newAPIError)
			if finalizeErr != nil {
				logger.LogError(c, "error finalizing failed stream billing: "+finalizeErr.Error())
			}
			if testChannel == nil {
				service.ChargeViolationFeeIfNeeded(c, relayInfo, newAPIError)
			}
		}
		if testChannel == nil && newAPIError != nil && finalization != relaycommon.BillingSettled && finalization != relaycommon.BillingSettledPartial {
			perfmetrics.RecordRelayResultSync(context.Background(), relayInfo, newAPIError)
		}
		conversationError := newAPIError
		if conversationError == nil {
			conversationError = clientResponseError
		}
		if testChannel == nil {
			service.RecordConversationLog(c, relayInfo, conversationError)
		}
		if newAPIError == nil {
			newAPIError = clientResponseError
		}
	}()

	request, err := helper.GetAndValidateRequest(c, relayFormat)
	requestReadFinishedAt := time.Now()
	if err != nil {
		// Map "request body too large" to 413 so clients can handle it correctly
		if common.IsRequestBodyTooLargeError(err) || errors.Is(err, common.ErrRequestBodyTooLarge) {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge, types.ErrOptionWithSkipRetry())
		} else {
			newAPIError = types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		return
	}

	relayInfo, err = relaycommon.GenRelayInfo(c, relayFormat, request, ws)
	if err != nil {
		newAPIError = types.NewError(err, types.ErrorCodeGenRelayInfoFailed)
		return
	}
	relayInfo.IsChannelTest = testChannel != nil
	if relayFormat != types.RelayFormatOpenAIRealtime {
		installRequestedModelResponseWriter(c, relayInfo)
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
	contextRequest := rootRequestForContextFallback(request)
	if contextRequest == nil {
		newAPIError = types.NewError(errors.New("request conversion for context fallback failed"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		return
	}
	meta, newAPIError = prepareContextFallback(c, relayInfo, contextRequest, tokens)
	if newAPIError != nil {
		return
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
	} else if !relayInfo.IsChannelTest {
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
		channel := testChannel
		var channelErr *types.NewAPIError
		if channel == nil {
			channel, channelErr = getChannel(c, relayInfo, retryParam)
		} else {
			service.RequestPolicy(c).BeginAttempt(channel, relayInfo.UsingGroup)
		}
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
			service.FinishChannelKeyProbe(c, true)
			if testChannel == nil {
				service.RecordChannelUpstreamResponseAsync(channel, http.StatusOK,
					common.GetContextKeyString(c, constant.ContextKeyChannelKey),
					common.GetContextKeyString(c, constant.ContextKeyChannelHealthModel))
			}
			relayInfo.LastError = nil
			// Response overrides may map one successful, billable upstream attempt
			// to a client 4xx/5xx without changing its internal success semantics.
			common.SetContextKey(c, constant.ContextKeyRelayUpstreamSucceeded, true)
			if finishErr := relaycommon.FinishFinalResponseWriter(c, true); finishErr != nil {
				newAPIError = types.NewError(finishErr, types.ErrorCodeBadResponse)
				return
			}
			clientResponseError = finalizeResponseOverride(c, relayInfo)
			return
		}
		if buffer := relaycommon.CurrentResponseOverrideBuffer(c); buffer != nil {
			buffer.MarkRelayError()
			buffer.Discard(c)
		} else {
			_ = relaycommon.FinishFinalResponseWriter(c, false)
		}
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			if relayInfo.StreamStatus != nil {
				relayInfo.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, requestErr)
			}
			return
		}

		newAPIError = service.NormalizeViolationFeeError(newAPIError)
		relayInfo.LastError = newAPIError
		// Tests report this exact channel/key; callers retain their existing health policy.
		if testChannel != nil {
			break
		}

		willRetry := shouldRetry(c, relayInfo, newAPIError, common.RetryTimes-retryParam.GetRetry())
		localBillingError := types.IsLocalBillingError(newAPIError)
		if localBillingError {
			willRetry = false
		}
		if relayInfo.IsContextFallbackActive() && relayInfo.ContextFallback.RouteMode == dto.ContextFallbackModeSame {
			willRetry = false
		}
		endpointMismatch := relayInfo.ProtocolEndpointMismatch &&
			relayInfo.ChannelRoutePlan != nil &&
			relayInfo.ChannelRoutePlan.RouteMode != hosttypes.ChannelRouteModeLegacy
		if !localBillingError && endpointMismatch {
			if retryParam.ExcludedChannelIDs == nil {
				retryParam.ExcludedChannelIDs = make(map[int]struct{})
			}
			retryParam.ExcludedChannelIDs[channel.Id] = struct{}{}
			willRetry = common.RetryTimes-retryParam.GetRetry() > 0 &&
				!retryBlockedByClientCommit(c, relayInfo) && !service.ShouldSkipRetryAfterChannelAffinityFailure(c) &&
				!(relayInfo.IsContextFallbackActive() && relayInfo.ContextFallback.RouteMode == dto.ContextFallbackModeSame)
		} else if !localBillingError {
			usingKey := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
			if _, real := newAPIError.GetUpstreamStatusCode(); real && channel.ChannelInfo.IsMultiKey {
				retryParam.ExcludeChannelKey(channel.Id, usingKey)
			}
			keyAction, keyHandled := service.HandleMultiKeyFailure(
				channel,
				common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex),
				usingKey,
				newAPIError,
				common.GetContextKeyString(c, constant.ContextKeyChannelHealthModel),
			)
			if keyHandled {
				channelScoped := service.IsMultiKeyFailureChannelScoped(channel,
					common.GetContextKeyString(c, constant.ContextKeyChannelHealthModel), newAPIError)
				retryParam.ExcludeChannelKey(channel.Id, usingKey)
				if channelScoped {
					if retryParam.ExcludedChannelIDs == nil {
						retryParam.ExcludedChannelIDs = make(map[int]struct{})
					}
					retryParam.ExcludedChannelIDs[channel.Id] = struct{}{}
				}
				if common.RetryTimes-retryParam.GetRetry() > 0 && !retryBlockedByClientCommit(c, relayInfo) &&
					!service.ShouldSkipRetryAfterChannelAffinityFailure(c) && !types.IsSkipRetryError(newAPIError) &&
					!(relayInfo.IsContextFallbackActive() && relayInfo.ContextFallback.RouteMode == dto.ContextFallbackModeSame) {
					willRetry = true
					if !channelScoped {
						retryParam.PreferredChannelID = channel.Id
					}
				}
				logger.LogInfo(c, fmt.Sprintf("multi-key failure handled: channel_id=%d, key_index=%d, action=%s", channel.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex), keyAction))
			} else {
				service.RecordChannelUpstreamErrorAsync(channel, newAPIError, usingKey,
					common.GetContextKeyString(c, constant.ContextKeyChannelHealthModel))
				processChannelError(c, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, usingKey, channel.GetAutoBan()), newAPIError, relayInfo)
			}
		}
		service.FinishChannelKeyProbe(c, false)
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
		rawFinalError = newAPIError
		if relayInfo.LastError == nil {
			relayInfo.LastError = newAPIError
		}
		if !types.IsClientErrorWritten(newAPIError) {
			newAPIError = resolveConfiguredFinalRelayError(c, relayInfo)
		}
	}
	return
}

// rootRequestForContextFallback adapts the standalone relaykit DTO to the
// host DTO used by the development-only context fallback planner.
func rootRequestForContextFallback(request dto.Request) taskdto.Request {
	if request == nil {
		return nil
	}
	body, err := common.Marshal(request)
	if err != nil {
		return nil
	}
	var converted taskdto.Request
	switch request.(type) {
	case *dto.GeneralOpenAIRequest:
		converted = &taskdto.GeneralOpenAIRequest{}
	case *dto.OpenAIResponsesRequest:
		converted = &taskdto.OpenAIResponsesRequest{}
	case *dto.OpenAIResponsesCompactionRequest:
		converted = &taskdto.OpenAIResponsesCompactionRequest{}
	case *dto.ClaudeRequest:
		converted = &taskdto.ClaudeRequest{}
	case *dto.GeminiChatRequest:
		converted = &taskdto.GeminiChatRequest{}
	default:
		return nil
	}
	if err := common.Unmarshal(body, converted); err != nil {
		return nil
	}
	return converted
}

// CountClaudeTokens implements Anthropic's token-counting utility endpoint.
// It deliberately skips upstream generation and billing; callers use this
// endpoint to size prompts before creating a Message.
func CountClaudeTokens(c *gin.Context) {
	request, err := helper.GetAndValidateClaudeRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)),
			},
		})
		return
	}
	if modalityErr := helper.ValidateRequestInputModalities(c, request.Model, request); modalityErr != nil {
		clientError := relayClientClaudeError(modalityErr, nil)
		clientError.Message = common.MessageWithRequestId(clientError.Message, c.GetString(common.RequestIdKey))
		c.JSON(modalityErr.StatusCode, gin.H{
			"type":  "error",
			"error": clientError,
		})
		return
	}

	info := relaycommon.GenRelayInfoClaude(c, request)
	inputTokens, err := service.CountRequestToken(c, request.GetTokenCountMeta(), info)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"input_tokens": inputTokens})
}

var upgrader = websocket.Upgrader{
	Subprotocols: []string{"realtime", "responses"}, // WS 握手支持的协议，如果有使用 Sec-WebSocket-Protocol，则必须在此声明对应的 Protocol
	CheckOrigin: func(r *http.Request) bool {
		return true // 允许跨域
	},
}

func writeRelayErrorResponse(c *gin.Context, relayFormat types.RelayFormat, ws *websocket.Conn, relayInfo *relaycommon.RelayInfo, newAPIError *types.NewAPIError) {
	if newAPIError == nil || types.IsClientErrorWritten(newAPIError) {
		return
	}
	if relayFormat == types.RelayFormatOpenAIRealtime {
		helper.WssError(c, ws, relayClientOpenAIError(newAPIError, relayInfo))
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
				Error: relayClientClaudeError(newAPIError, relayInfo),
			})
			return
		}
		if relayFormat == types.RelayFormatOpenAIResponses {
			openAIError := relayClientOpenAIError(newAPIError, relayInfo)
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
		if err := helper.ObjectData(c, gin.H{"error": relayClientOpenAIError(newAPIError, relayInfo)}); err != nil {
			logger.LogError(c, "write stream error failed: "+err.Error())
		}
		return
	}

	if relayFormat == types.RelayFormatClaude {
		prepareRelayErrorHeaders(c)
		c.JSON(newAPIError.StatusCode, gin.H{
			"type":  "error",
			"error": relayClientClaudeError(newAPIError, relayInfo),
		})
		return
	}
	if relayFormat == types.RelayFormatGemini {
		prepareRelayErrorHeaders(c)
		c.JSON(newAPIError.StatusCode, dto.GeminiErrorResponse{
			Error: dto.GeminiError{
				Code:    newAPIError.StatusCode,
				Message: relayClientOpenAIError(newAPIError, relayInfo).Message,
				Status:  geminiCanonicalErrorStatus(newAPIError.StatusCode),
			},
		})
		return
	}
	prepareRelayErrorHeaders(c)
	c.JSON(newAPIError.StatusCode, gin.H{
		"error": relayClientOpenAIError(newAPIError, relayInfo),
	})
}

func relayClientOpenAIError(newAPIError *types.NewAPIError, relayInfo *relaycommon.RelayInfo) types.OpenAIError {
	openAIError := newAPIError.ToOpenAIError()
	openAIError.Message = relaycommon.SanitizeClientModelErrorMessage(newAPIError.MaskSensitiveError(), relayInfo)
	return openAIError
}

func relayClientClaudeError(newAPIError *types.NewAPIError, relayInfo *relaycommon.RelayInfo) types.ClaudeError {
	claudeError := newAPIError.ToClaudeError()
	claudeError.Message = relaycommon.SanitizeClientModelErrorMessage(newAPIError.MaskSensitiveError(), relayInfo)
	return claudeError
}

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

func prepareRelayErrorHeaders(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	for _, key := range []string{"Content-Length", "Content-Encoding", "ETag", "Content-Range", "Transfer-Encoding"} {
		c.Writer.Header().Del(key)
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
}

func addUsedChannel(c *gin.Context, channelId int) {
	useChannel := c.GetStringSlice("use_channel")
	useChannel = append(useChannel, fmt.Sprintf("%d", channelId))
	c.Set("use_channel", useChannel)
}

func relayRetryGroup(info *relaycommon.RelayInfo) string {
	if info != nil && info.SubscriptionEntitlementGroup != "" {
		return info.SubscriptionEntitlementGroup
	}
	if info == nil {
		return ""
	}
	return info.TokenGroup
}

func shouldRetry(c *gin.Context, relayInfo *relaycommon.RelayInfo, openaiErr *types.NewAPIError, retryTimes int) bool {
	if openaiErr == nil {
		return false
	}
	if retryBlockedByClientCommit(c, relayInfo) {
		// Legacy Claude requests flush SSE headers before receiving the upstream HTTP status.
		// Only a real HTTP failure before any stream event may bypass that header commit.
		upstreamStatus, real := openaiErr.GetUpstreamStatusCode()
		if !real || upstreamStatus < http.StatusBadRequest || upstreamStatus > 599 ||
			relayInfo == nil || !relayInfo.IsStream || relayInfo.ChannelMeta == nil ||
			relayInfo.ChannelType != constant.ChannelTypeAnthropic ||
			relayInfo.ChannelRoutePlan == nil || relayInfo.ChannelRoutePlan.RouteMode != hosttypes.ChannelRouteModeLegacy ||
			(relayInfo.RelayFormat != types.RelayFormatClaude && relayInfo.RelayFormat != types.RelayFormatOpenAI) ||
			relayInfo.StreamStatus == nil || relayInfo.ReceivedResponseCount != 0 || relayInfo.SendResponseCount != 0 {
			return false
		}
		status := relayInfo.StreamStatus
		endReason, _ := status.End()
		if c.Request.Context().Err() != nil || endReason != relaycommon.StreamEndReasonNone ||
			status.ClientPayloadIsCommitted() || status.ErrorFrameIsWritten() {
			return false
		}
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

func retryBlockedByClientCommit(c *gin.Context, relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo != nil && relayInfo.IsStream && relayInfo.StreamStatus != nil {
		status := relayInfo.StreamStatus
		if status.RetryCommitPolicy() == relaycommon.StreamRetryCommitPolicyPayload &&
			relayInfo.ReceivedResponseCount > 0 &&
			!status.ClientPayloadIsCommitted() && !status.ErrorFrameIsWritten() {
			// An unclassified upstream event may already have been forwarded by a legacy handler.
			return true
		}
		httpCommitted := c != nil && c.Writer != nil && c.Writer.Written()
		return relayInfo.StreamStatus.RetryBlocked(httpCommitted)
	}
	return c != nil && c.Writer != nil && c.Writer.Written()
}

func resolveConfiguredFinalRelayError(c *gin.Context, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	if relayInfo != nil && types.IsLocalBillingError(relayInfo.LastError) {
		// 本地额度和数据故障保留各自状态码，避免被渠道 final_error 覆盖。
		return relayInfo.LastError
	}
	// Pre-send truncation failures are local request errors, not an upstream outage.
	// Keep the classification so unmatched errors can use a safe client message
	// after channel and global final_error overrides have had a chance to apply.
	isLocalContextError := false
	if relayInfo != nil && relayInfo.LastError != nil && relayInfo.ContextTruncation != nil && relayInfo.ContextTruncation.Enabled {
		code := string(relayInfo.LastError.GetErrorCode())
		_, upstream := relayInfo.LastError.GetUpstreamStatusCode()
		if !upstream && relayInfo.LastError.StatusCode == http.StatusBadRequest &&
			(code == "context_length_exceeded" || strings.HasPrefix(code, "context_truncation_")) {
			isLocalContextError = true
		}
	}
	// Local conversion failures must not be presented as upstream congestion.
	if relayInfo != nil && relayInfo.LastError != nil &&
		relayInfo.LastError.GetErrorCode() == types.ErrorCodeConvertRequestFailed {
		return types.NewErrorWithStatusCode(
			errors.New("request protocol conversion failed; verify the request fields and channel protocol compatibility"),
			types.ErrorCodeConvertRequestFailed,
			relayInfo.LastError.StatusCode,
			types.ErrOptionWithSkipRetry(),
		)
	}

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
	// Responses clients rely on the provider's 4xx classification for request
	// and credential errors. Preserve it after channel overrides but before the
	// global outage fallback turns every error into 502.
	if relayInfo != nil && relayInfo.RelayFormat == types.RelayFormatOpenAIResponses && relayInfo.LastError != nil {
		if status, upstream := relayInfo.LastError.GetUpstreamStatusCode(); upstream && status >= 400 && status < 500 {
			return relayInfo.LastError
		}
	}

	mapped, matched, err := relaycommon.ApplyFinalErrorOverride(operation_setting.GetDefaultFinalErrorOverride(), relayInfo)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("invalid default final_error override: %s", err.Error()))
	} else if matched {
		return mapped
	}

	if isLocalContextError {
		return types.NewErrorWithStatusCode(
			errors.New("请求上下文过长，请减少输入内容后重试。"),
			relayInfo.LastError.GetErrorCode(),
			relayInfo.LastError.StatusCode,
			types.ErrOptionWithSkipRetry(),
		)
	}

	return types.NewOpenAIError(
		errors.New(http.StatusText(http.StatusBadGateway)),
		types.ErrorCodeBadResponse,
		http.StatusBadGateway,
		types.ErrOptionWithSkipRetry(),
	)
}

func getChannel(c *gin.Context, info *relaycommon.RelayInfo, retryParam *service.RetryParam) (*model.Channel, *types.NewAPIError) {
	modelName := info.GetAttemptModelName()
	if modelName == "" {
		modelName = retryParam.ModelName
	}
	if info.ContextFallback != nil && info.ContextFallback.Applied &&
		info.ContextFallback.RouteMode == dto.ContextFallbackModeSame &&
		info.ContextFallback.TargetChannelID == info.ContextFallback.SourceChannelID {
		channel, err := model.GetChannelById(info.ContextFallback.SourceChannelID, true)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		service.RequestPolicy(c).BeginAttempt(channel, info.UsingGroup)
		return channel, nil
	}
	if info.ChannelMeta == nil {
		autoBan := c.GetBool("auto_ban")
		autoBanInt := 1
		if !autoBan {
			autoBanInt = 0
		}
		channel := &model.Channel{
			Id:      c.GetInt("channel_id"),
			Type:    c.GetInt("channel_type"),
			Name:    c.GetString("channel_name"),
			AutoBan: &autoBanInt,
		}
		if snapshot, ok := common.GetContextKeyType[*model.Channel](c, constant.ContextKeyChannelSnapshot); ok && snapshot != nil && model.DB == nil {
			service.RequestPolicy(c).BeginAttempt(snapshot, info.UsingGroup)
			return snapshot, nil
		}
		// Reload middleware-selected multi-key channels so failure handling sees key state.
		if channel.Id > 0 {
			selected, loadErr := model.GetChannelById(channel.Id, true)
			if loadErr != nil {
				// The distributor snapshot is sufficient to finish the in-flight
				// request when the backing DB is already closing (tests and shutdown
				// races); do not turn a selected channel into a routing failure.
				if snapshot, ok := common.GetContextKeyType[*model.Channel](c, constant.ContextKeyChannelSnapshot); ok && snapshot != nil {
					service.RequestPolicy(c).BeginAttempt(snapshot, info.UsingGroup)
					return snapshot, nil
				}
				return nil, types.NewError(loadErr, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
			}
			if selected != nil && selected.ChannelInfo.IsMultiKey {
				if retryParam.GetRetry() > 0 {
					if setupErr := middleware.SetupContextForSelectedChannelWithKeyExclusions(c, selected, modelName, retryParam.ExcludedChannelKeys(channel.Id)); setupErr != nil {
						return nil, setupErr
					}
				}
				service.RequestPolicy(c).BeginAttempt(selected, info.UsingGroup)
				return selected, nil
			}
		}
		service.RequestPolicy(c).BeginAttempt(channel, info.UsingGroup)
		return channel, nil
	}
	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(retryParam)
	if err != nil {
		return nil, types.NewError(errors.New(i18n.T(c, i18n.MsgRelayRetryGetChannelFailed, map[string]any{"Group": selectGroup, "Model": info.OriginModelName, "Error": err.Error()})), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}
	if channel == nil {
		return nil, types.NewError(errors.New(i18n.T(c, i18n.MsgRelayRetryNoAvailableChannel, map[string]any{"Group": selectGroup, "Model": info.OriginModelName})), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	}

	info.PriceData.GroupRatioInfo = helper.HandleGroupRatio(c, info)

	service.RequestPolicy(c).BeginAttempt(channel, selectGroup)
	newAPIError := middleware.SetupContextForSelectedChannelWithKeyExclusions(c, channel, modelName, retryParam.ExcludedChannelKeys(channel.Id))
	if newAPIError != nil {
		return nil, newAPIError
	}
	if modalityErr := helper.ValidateRequestInputModalities(c, modelName, info.Request); modalityErr != nil {
		return nil, modalityErr
	}
	return channel, nil
}

func processChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	service.ProcessChannelError(c, channelError, err, relayInfo)
}

func RelayMidjourney(c *gin.Context) {
	policy := service.RequestPolicy(c)
	defer func() {
		if policy.Attempts > 0 && !policy.Successful {
			service.RecordRequestPolicyTermination(c, types.NewErrorWithStatusCode(errors.New("Midjourney submission failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway, types.ErrOptionWithSkipRetry()))
		}
	}()
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatMjProxy, nil, nil)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"description": fmt.Sprintf("failed to generate relay info: %s", err.Error()),
			"type":        "upstream_error",
			"code":        4,
		})
		return
	}

	var mjErr *taskdto.MidjourneyResponse
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
		policy.Successful = false
		statusCode := http.StatusBadRequest
		if mjErr.Code == 30 {
			mjErr.Result = i18n.T(c, i18n.MsgRelayGroupSaturated)
			statusCode = http.StatusTooManyRequests
		}
		c.JSON(statusCode, gin.H{
			"description": fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result),
			"type":        "upstream_error",
			"code":        mjErr.Code,
		})
		channelId := c.GetInt("channel_id")
		logger.LogError(c, common.LogText("relay error (channel #%d, status code %d): %s", channelId, statusCode, fmt.Sprintf("%s %s", mjErr.Description, mjErr.Result)))
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
	// The web fallback may already have applied static-asset cache headers.
	// A missing API or asset can appear after an upgrade; never cache its 404.
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
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

// RelayTaskPluginEndpoint keeps unclaimed shared-endpoint traffic on its
// existing handler while claimed requests enter the generation-pinned
// host-owned protocol bridge.
func RelayTaskPluginEndpoint(c *gin.Context, fallback gin.HandlerFunc) {
	pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint)
	if !exists {
		fallback(c)
		return
	}
	pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint)
	if !ok || pinned.Plugin == nil || pinned.Generation == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": "Task protocol request failed",
				"type":    "new_api_error",
				"code":    "task_protocol_error",
			},
		})
		return
	}
	switch pinned.Protocol {
	case "openai_responses":
		serveTaskPluginProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
	case pluginruntime.ProtocolOpenAIImage:
		serveTaskPluginImageProtocol(c, pinned, defaultPluginProtocolBridgeDeps())
	default:
		fallback(c)
	}
}

func RelayTaskFetch(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if relayInfo.RelayMode == relayconstant.RelayModeUnknown {
		// Retrieval endpoints read the persisted task and do not need channel routing.
		relayInfo.RelayMode = relayconstant.RelayModeVideoFetchByID
	}
	if taskErr := relay.RelayTaskFetch(c, relayInfo.RelayMode); taskErr != nil {
		respondTaskError(c, taskErr)
	}
}

type taskSubmissionOutcome struct {
	Result    *relay.TaskSubmitResult
	Task      *model.Task
	RelayInfo *relaycommon.RelayInfo
}

func RelayTask(c *gin.Context) {
	relayInfo, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
	if err != nil {
		respondTaskSubmissionError(c, &taskdto.TaskError{
			Code:       "gen_relay_info_failed",
			Message:    err.Error(),
			StatusCode: http.StatusInternalServerError,
		})
		return
	}
	if action := c.GetString("task_action"); action != "" {
		relayInfo.Action = action
	}

	if taskErr := relay.ResolveOriginTask(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	if taskErr := relay.ApplyOriginTaskAffinity(c, relayInfo); taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}

	outcome, taskErr := executeTaskSubmission(c, relayInfo)
	if taskErr != nil {
		respondTaskSubmissionError(c, taskErr)
		return
	}
	presentTaskSubmission(c, outcome)
}

// executeTaskSubmission owns the retry, billing, and persistence lifecycle.
// It deliberately performs no client response writes so JSON and protocol
// presenters share the same durable task barrier. Its cancellation semantics
// come from c.Request.Context: native task endpoints use the client context,
// while the Responses bridge supplies an independently bounded context.
func executeTaskSubmission(c *gin.Context, relayInfo *relaycommon.RelayInfo) (*taskSubmissionOutcome, *taskdto.TaskError) {
	return executeTaskSubmissionWith(c, relayInfo, relay.RelayTaskSubmit)
}

type taskSubmitAttempt func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *taskdto.TaskError)

func executeTaskSubmissionWith(
	c *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	submit taskSubmitAttempt,
) (*taskSubmissionOutcome, *taskdto.TaskError) {
	policy := service.RequestPolicy(c)
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	diagnostics.start(relayInfo)
	var result *relay.TaskSubmitResult
	var taskErr *taskdto.TaskError
	durable := false
	stage := "start"
	defer func() {
		if !durable && relayInfo.Billing != nil {
			diagnostics.refund(stage)
			relayInfo.Billing.Refund(c)
		}
	}()
	stage = "before_attempt"
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_attempt", 0)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	retryParam := &service.RetryParam{
		Ctx:         c,
		TokenGroup:  relayInfo.TokenGroup,
		ModelName:   relayInfo.OriginModelName,
		RequestPath: c.Request.URL.Path,
		Retry:       common.GetPointer(0),
	}

	for ; retryParam.GetRetry() <= common.RetryTimes; retryParam.IncreaseRetry() {
		stage = "select_channel"
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("before_attempt", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		var channel *model.Channel

		if lockedCh, ok := relayInfo.LockedChannel.(*model.Channel); ok && lockedCh != nil {
			channel = lockedCh
			policy.BeginAttempt(channel, relayInfo.UsingGroup)
			if retryParam.GetRetry() > 0 {
				if setupErr := middleware.SetupContextForSelectedChannel(c, channel, relayInfo.OriginModelName); setupErr != nil {
					taskErr = service.TaskErrorWrapperLocal(setupErr.Err, "setup_locked_channel_failed", http.StatusInternalServerError)
					break
				}
			}
		} else {
			var channelErr *types.NewAPIError
			channel, channelErr = getChannel(c, relayInfo, retryParam)
			if channelErr != nil {
				logger.LogError(c, channelErr.Error())
				taskErr = service.TaskErrorWrapperLocal(channelErr.Err, "get_channel_failed", channelErr.StatusCode)
				break
			}
		}
		diagnostics.attempt(retryParam.GetRetry()+1, channel, relayInfo.LockedChannel != nil)

		service.AppendUsedChannel(c, channel.Id)
		bodyStorage, bodyErr := common.GetBodyStorage(c)
		if bodyErr != nil {
			stage = "read_body"
			if common.IsRequestBodyTooLargeError(bodyErr) || errors.Is(bodyErr, common.ErrRequestBodyTooLarge) {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusRequestEntityTooLarge)
			} else {
				taskErr = service.TaskErrorWrapperLocal(bodyErr, "read_request_body_failed", http.StatusBadRequest)
			}
			break
		}
		c.Request.Body = io.NopCloser(bodyStorage)

		stage = "submit"
		result, taskErr = submit(c, relayInfo)
		if requestErr := c.Request.Context().Err(); requestErr != nil {
			diagnostics.cancelled("after_submit", retryParam.GetRetry()+1)
			taskErr = service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
			break
		}
		if taskErr == nil {
			diagnostics.attemptSucceeded(retryParam.GetRetry()+1, result)
			break
		}

		taskAPIError := taskSubmissionAPIError(taskErr)
		relayInfo.LastError = taskAPIError
		decision := decideTaskRetry(c, taskErr, common.RetryTimes-retryParam.GetRetry())
		service.RecordPolicyFailure(c, channel.Id, taskAPIError, decision)
		if !taskErr.LocalError {
			processChannelError(c,
				*types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey,
					common.GetContextKeyString(c, constant.ContextKeyChannelKey), channel.GetAutoBan()),
				taskAPIError,
				relayInfo)
		}

		willRetry := decision.Action == "retry"
		diagnostics.attemptFailed(retryParam.GetRetry()+1, channel, taskErr, willRetry)
		if !willRetry {
			break
		}
	}

	useChannel := c.GetStringSlice("use_channel")
	if len(useChannel) > 1 {
		retryLogStr := common.LogText("retry: %s", strings.Trim(strings.Join(strings.Fields(fmt.Sprint(useChannel)), "->"), "[]"))
		logger.LogInfo(c, retryLogStr)
	}

	if taskErr != nil {
		diagnostics.failed(stage, "task_error", taskErr, false)
		return nil, taskErr
	}
	if result == nil {
		taskErr = service.TaskErrorWrapperLocal(errors.New("task submission returned no result"), "task_submit_failed", http.StatusInternalServerError)
		diagnostics.failed("submit", "missing_result", taskErr, false)
		return nil, taskErr
	}
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_reserve", retryParam.GetRetry()+1)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	// Reserve any submit-time upward billing adjustment before persistence.
	// This keeps insertion failures fully refundable while ensuring settlement
	// after the barrier normally has a zero positive delta.
	if relayInfo.Billing != nil {
		stage = "reserve"
		diagnostics.reserve("reserve_start", result.Quota)
		if reserveErr := relayInfo.Billing.Reserve(result.Quota); reserveErr != nil {
			common.SysError(common.LogText("reserve adjusted task billing error: %s", reserveErr.Error()))
			taskErr = service.TaskErrorWrapperLocal(errors.New("insufficient quota for adjusted task cost"), string(types.ErrorCodeInsufficientUserQuota), http.StatusForbidden)
			diagnostics.failed("reserve", "insufficient_quota", taskErr, false)
			return nil, taskErr
		}
		diagnostics.reserve("reserve_complete", result.Quota)
	}
	if requestErr := c.Request.Context().Err(); requestErr != nil {
		diagnostics.cancelled("before_insert", retryParam.GetRetry()+1)
		return nil, service.TaskErrorWrapperLocal(requestErr, "request_cancelled", http.StatusRequestTimeout)
	}

	stage = "insert"
	task := model.InitTask(result.Platform, relayInfo)
	task.PrivateData.Execution = service.TaskExecutionSnapshotFromContext(c)
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
		TieredSnapshot:  relayInfo.TieredBillingSnapshot,
	}
	task.Quota = result.Quota
	task.Data = result.TaskData
	if len(result.PluginState) > 0 {
		task.PrivateData.PluginState = result.PluginState
	}
	task.Action = relayInfo.Action
	if immediate := result.Immediate; immediate != nil {
		task.Status = model.TaskStatus(immediate.Status)
		task.Progress = immediate.Progress
		if immediate.Status == model.TaskStatusSuccess || immediate.Status == model.TaskStatusFailure {
			task.FinishTime = time.Now().Unix()
		}
		if immediate.Status == model.TaskStatusFailure {
			task.FailReason = immediate.Reason
		}
		if immediate.Url != "" {
			task.PrivateData.ResultURL = immediate.Url
		} else if immediate.Status == model.TaskStatusSuccess {
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
	}
	// A native submit route may declare retainResult: false. It applies only
	// to immediate terminal results: the client receives the complete response
	// once, the upstream snapshot is never persisted, and the task is not
	// retrievable afterwards. An asynchronous result on such a route keeps its
	// snapshot because polling and retrieval need it. The OpenAI Images
	// protocol delivers its images inline in the same HTTP response, so its
	// immediate results follow the same rule; an asynchronous image task is
	// expected there and is polled inside the request.
	var insertOmits []string
	immediateTerminal := result.Immediate != nil && (result.Immediate.Status == model.TaskStatusSuccess || result.Immediate.Status == model.TaskStatusFailure)
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		pinned, ok := pinnedValue.(pluginruntime.PinnedRoute)
		if ok && pinned.Route.RetainResult != nil && !*pinned.Route.RetainResult {
			if immediateTerminal {
				task.PrivateData.ResultDiscarded = true
				insertOmits = append(insertOmits, "data")
			} else {
				logger.LogWarn(c, common.LogText("task plugin route %s %s declares retainResult: false but returned an asynchronous result; retaining task %s", pinned.Route.Method, pinned.Route.Path, task.TaskID))
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists && immediateTerminal {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == pluginruntime.ProtocolOpenAIImage {
			task.PrivateData.ResultDiscarded = true
			insertOmits = append(insertOmits, "data")
		}
	}
	diagnostics.insertStart(task)
	if insertErr := task.InsertWithContext(c.Request.Context(), insertOmits...); insertErr != nil {
		common.SysError(common.LogText("insert task error: %s", insertErr.Error()))
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to persist task"), "task_insert_failed", http.StatusInternalServerError)
		diagnostics.failed("insert", "database_error", taskErr, false)
		return nil, taskErr
	}
	durable = true
	stage = "settle"
	diagnostics.durable(task)
	diagnostics.settleStart(task, result.Quota)

	if settleErr := service.SettleBilling(c, relayInfo, result.Quota); settleErr != nil {
		common.SysError(common.LogText("settle task billing error: %s", settleErr.Error()))
		taskErr = service.TaskErrorWrapperLocal(errors.New("failed to settle task billing"), "task_billing_settlement_failed", http.StatusInternalServerError)
		diagnostics.failed("settle", "billing_error", taskErr, true)
		return nil, taskErr
	}
	if task.Status != model.TaskStatusFailure {
		service.MarkRequestPolicySuccess(c, nil)
	} else {
		policy.AddEvent(service.PolicyEvent{Decision: service.PolicyDecision{Action: "stop", Reason: "task_failed", Source: "upstream"}})
	}
	service.LogTaskConsumption(c, relayInfo, task)
	diagnostics.complete(task, result.Quota)

	return &taskSubmissionOutcome{Result: result, Task: task, RelayInfo: relayInfo}, nil
}

func presentTaskSubmission(c *gin.Context, outcome *taskSubmissionOutcome) {
	diagnostics := newTaskPluginSubmitDiagnostics(c)
	otherRatios := outcome.RelayInfo.PriceData.OtherRatios()
	if otherRatios == nil {
		otherRatios = map[string]float64{}
	}
	if ratiosJSON, err := common.Marshal(otherRatios); err == nil {
		c.Header("X-New-Api-Other-Ratios", string(ratiosJSON))
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedRoute); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedRoute); ok && pinned.Plugin != nil && pinned.Route.Render != "" {
			view, err := service.BuildTaskPluginView(outcome.Task)
			requestValue, _ := c.Get(pluginruntime.ContextKeyRouteRequest)
			requestContext, _ := requestValue.(pluginruntime.RouteRequestContext)
			if err == nil {
				viewValue, valueErr := taskPluginProtocolJSONValue(view)
				if valueErr == nil {
					if body, callErr := pinned.Plugin.Engine.CallPath(c.Request.Context(), "native", []string{pinned.Route.Render}, requestContext.JSValue(), viewValue); callErr == nil {
						diagnostics.present(outcome.Task, "native_presenter")
						c.JSON(http.StatusOK, body)
						return
					} else {
						logger.LogError(c, common.LogText("task plugin native submit presenter failed: %s", callErr.Error()))
					}
				} else {
					logger.LogError(c, common.LogText("encode task plugin native submit view failed: %s", valueErr.Error()))
				}
			} else {
				logger.LogError(c, common.LogText("build task plugin native submit view failed: %s", err.Error()))
			}
		}
	}
	if pinnedValue, exists := c.Get(pluginruntime.ContextKeyPinnedEndpoint); exists {
		if pinned, ok := pinnedValue.(pluginruntime.PinnedEndpoint); ok && pinned.Protocol == "openai_video" && pinned.Operation.Name == "create" {
			diagnostics.present(outcome.Task, "openai_video_create")
			c.JSON(http.StatusOK, outcome.Task.ToOpenAIVideo())
			return
		}
	}
	createdAt := outcome.Task.CreatedAt
	if createdAt == 0 {
		createdAt = outcome.Task.SubmitTime
	}
	diagnostics.present(outcome.Task, "host_fallback")
	c.JSON(http.StatusOK, map[string]any{
		"id":         outcome.Task.TaskID,
		"task_id":    outcome.Task.TaskID,
		"status":     "queued",
		"model":      outcome.RelayInfo.OriginModelName,
		"created_at": createdAt,
	})
}

func respondTaskSubmissionError(c *gin.Context, taskErr *taskdto.TaskError) {
	service.RecordRequestPolicyTermination(c, taskSubmissionAPIError(taskErr))
	newTaskPluginSubmitDiagnostics(c).presentError(taskErr)
	if middleware.RespondTaskPluginError(c, taskErr) {
		return
	}
	respondTaskError(c, taskErr)
}

// respondTaskError 统一输出 Task 错误响应（含 429 限流提示改写）
func respondTaskError(c *gin.Context, taskErr *taskdto.TaskError) {
	if taskErr.StatusCode == http.StatusTooManyRequests {
		taskErr.Message = i18n.T(c, i18n.MsgRelayGroupUpstreamSaturated)
	}
	c.JSON(taskErr.StatusCode, taskErr)
}

// taskSubmissionAPIError adapts a task error for the shared relay error paths.
// TaskError.Error is nil for many local rejections, so fall back to the message.
func taskSubmissionAPIError(taskErr *taskdto.TaskError) *types.NewAPIError {
	err := taskErr.Error
	if err == nil {
		err = errors.New(taskErr.Message)
	}
	return types.NewOpenAIError(err, types.ErrorCodeBadResponseStatusCode, taskErr.StatusCode)
}

// decideTaskRetry is the single retry decision for task submissions. The
// reason is recorded in the request policy decision events of the log details.
func decideTaskRetry(c *gin.Context, taskErr *taskdto.TaskError, retryTimes int) service.PolicyDecision {
	stop := service.PolicyDecision{Action: "stop", Source: "system"}
	retry := service.PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "system"}
	switch {
	case taskErr == nil:
		stop.Reason = "request_completed"
	case taskErr.NoRetry:
		stop.Reason = "task_accepted"
	case service.ShouldSkipRetryAfterChannelAffinityFailure(c):
		stop.Reason, stop.Source = "strict_session", "session_rule"
		if source := service.RequestPolicy(c).SessionModeSource; source != "" {
			stop.Source = source
		}
	case retryTimes <= 0:
		stop.Reason, stop.Source = "attempt_budget_exhausted", "global"
	case service.GetChannelConstraints(c).SuppressesRetry():
		stop.Reason, stop.Source = "pinned_channel", "channel_constraint"
	case taskErr.StatusCode == http.StatusTooManyRequests, taskErr.StatusCode == 307:
		return retry
	case taskErr.StatusCode/100 == 5:
		// 超时不重试
		if operation_setting.IsAlwaysSkipRetryStatusCode(taskErr.StatusCode) {
			stop.Reason = "system_retry_exclusion"
			break
		}
		return retry
	case taskErr.StatusCode == http.StatusBadRequest, taskErr.StatusCode == 408:
		// azure处理超时不重试
		stop.Reason = "status_not_retryable"
	case taskErr.LocalError:
		stop.Reason = "local_rejection"
	case taskErr.StatusCode/100 == 2:
		stop.Reason = "system_retry_exclusion"
	default:
		return retry
	}
	return stop
}

// CountTokens 保留旧路由名称，复用 Claude 本地 token 统计实现。
func CountTokens(c *gin.Context) {
	CountClaudeTokens(c)
}

func shouldRetryTaskRelay(c *gin.Context, _ int, taskErr *taskdto.TaskError, retryTimes int) bool {
	if (c.Writer != nil && c.Writer.Written()) || (c.Request != nil && c.Request.Context().Err() != nil) || taskErr == nil || taskErr.SkipRetry {
		return false
	}
	if service.ShouldSkipRetryAfterChannelAffinityFailure(c) || retryTimes <= 0 {
		return false
	}
	if _, ok := c.Get("specific_channel_id"); ok {
		return false
	}
	switch {
	case taskErr.StatusCode == http.StatusTooManyRequests, taskErr.StatusCode == http.StatusTemporaryRedirect:
		return true
	case taskErr.StatusCode/100 == 5:
		return !operation_setting.IsAlwaysSkipRetryStatusCode(taskErr.StatusCode)
	case taskErr.StatusCode == http.StatusBadRequest, taskErr.StatusCode == http.StatusRequestTimeout:
		return false
	case taskErr.LocalError, taskErr.StatusCode/100 == 2:
		return false
	default:
		return true
	}
}

func fastTokenCountMetaForPricing(request interface{ GetTokenCountMeta() *types.TokenCountMeta }) *types.TokenCountMeta {
	if request == nil {
		return &types.TokenCountMeta{TokenType: types.TokenTypeTokenizer}
	}
	return request.GetTokenCountMeta()
}
