package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	legacydto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/service"
	legacyconvert "github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/service/relaynormalize"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// isConvertedProtocolRoute reports whether the current attempt uses automatic conversion.
func isConvertedProtocolRoute(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelRoutePlan != nil && info.ChannelRoutePlan.RouteMode == types.ChannelRouteModeConverted
}

// isUnconvertedProtocolRoute reports whether the current attempt preserves the client wire protocol.
func isUnconvertedProtocolRoute(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelRoutePlan == nil {
		return false
	}
	mode := info.ChannelRoutePlan.RouteMode
	return mode == types.ChannelRouteModeNative || mode == types.ChannelRouteModeNormalized
}

// isNormalizedProtocolRoute reports whether the current attempt requires final-wire normalization.
func isNormalizedProtocolRoute(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelRoutePlan != nil && info.ChannelRoutePlan.RouteMode == types.ChannelRouteModeNormalized
}

// executeConvertedTextRoute converts the request, calls the planned upstream endpoint, and converts the response back.
func executeConvertedTextRoute(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request any) (*dto.Usage, *types.NewAPIError) {
	if info == nil || info.ChannelRoutePlan == nil {
		return nil, types.NewError(fmt.Errorf("protocol route plan is missing"), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	plan := info.ChannelRoutePlan
	if plan.RouteMode != types.ChannelRouteModeConverted {
		return nil, types.NewError(fmt.Errorf("protocol route is not converted"), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	info.ProtocolEndpointMismatch = false

	converted, err := convertProtocolRequest(c, info, plan, request)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	info.FinalRequestRelayFormat = plan.UpstreamRelayFormat

	body, closer, requestError := prepareTextRouteRequest(c, info, converted.Value)
	if requestError != nil {
		return nil, requestError
	}
	defer closer.Close()

	upstreamPath, ok := service.BuildTextProtocolPath(plan.UpstreamEndpointType, info.UpstreamModelName, info.IsStream)
	if !ok {
		return nil, types.NewError(fmt.Errorf("unsupported upstream endpoint type: %s", plan.UpstreamEndpointType), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	plan.UpstreamPath = upstreamPath
	clientPath := info.RequestURLPath
	clientMode := info.RelayMode
	info.RequestURLPath = upstreamPath
	info.RelayMode = plan.UpstreamRelayMode
	respValue, err := adaptor.DoRequest(c, info, body)
	info.RequestURLPath = clientPath
	info.RelayMode = clientMode
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResp, ok := respValue.(*http.Response)
	if !ok || httpResp == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid upstream response type %T", respValue), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	if httpResp.StatusCode != http.StatusOK {
		info.ProtocolEndpointMismatch = httpResp.StatusCode == http.StatusNotFound || httpResp.StatusCode == http.StatusMethodNotAllowed
		newAPIError := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newAPIError, c.GetString("status_code_mapping"))
		return nil, newAPIError
	}
	if info.IsStream {
		return handleConvertedTextStream(c, info, httpResp)
	}
	return handleConvertedTextResponse(c, info, httpResp)
}

// convertProtocolRequest keeps legacy root DTO callers on the mature host
// converter while feeding the independent relaykit converter for native DTOs.
func convertProtocolRequest(c *gin.Context, info *relaycommon.RelayInfo, plan *types.ChannelRoutePlan, request any) (*relayconvert.RequestResult, error) {
	if isLegacyProtocolRequest(request) {
		legacyResult, err := legacyconvert.ConvertRequestByID(c, info, plan.RequestConverter, request)
		if err != nil {
			return nil, err
		}
		if c != nil {
			c.Set("legacy_protocol_dto", true)
		}
		adapted, err := adaptProtocolRequest(legacyResult.Value, plan.UpstreamRelayFormat)
		if err != nil {
			return nil, err
		}
		return &relayconvert.RequestResult{
			Value:     adapted,
			From:      plan.ClientRelayFormat,
			To:        plan.UpstreamRelayFormat,
			Converter: legacyResult.Converter,
			Quality:   relayconvert.RequestConverterQuality(legacyResult.Quality),
		}, nil
	}
	adapted, err := adaptProtocolRequest(request, plan.ClientRelayFormat)
	if err != nil {
		return nil, err
	}
	return relayconvert.ConvertRequestByID(c, info, plan.RequestConverter, adapted)
}

func isLegacyProtocolRequest(request any) bool {
	switch request.(type) {
	case *legacydto.GeneralOpenAIRequest, legacydto.GeneralOpenAIRequest,
		*legacydto.OpenAIResponsesRequest, legacydto.OpenAIResponsesRequest,
		*legacydto.ClaudeRequest, legacydto.ClaudeRequest,
		*legacydto.GeminiChatRequest, legacydto.GeminiChatRequest:
		return true
	default:
		return false
	}
}

// adaptProtocolRequest normalizes legacy root DTO values to the relaykit DTOs
// consumed by the protocol conversion registry.
func adaptProtocolRequest(request any, format types.RelayFormat) (any, error) {
	var adapted any
	switch format {
	case types.RelayFormatOpenAI:
		adapted = &dto.GeneralOpenAIRequest{}
	case types.RelayFormatOpenAIResponses:
		adapted = &dto.OpenAIResponsesRequest{}
	case types.RelayFormatClaude:
		adapted = &dto.ClaudeRequest{}
	case types.RelayFormatGemini:
		adapted = &dto.GeminiChatRequest{}
	default:
		return request, nil
	}
	data, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	if err := common.Unmarshal(data, adapted); err != nil {
		return nil, err
	}
	return adapted, nil
}

// executeNativeTextRoute sends Messages or GenerateContent through a standard compatible channel without format conversion.
func executeNativeTextRoute(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request any, passThrough bool) (*dto.Usage, *types.NewAPIError) {
	if !isUnconvertedProtocolRoute(info) {
		return nil, types.NewError(fmt.Errorf("native protocol route plan is missing"), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	plan := info.ChannelRoutePlan
	if plan.RouteMode == types.ChannelRouteModeNormalized {
		passThrough = false
	}
	info.ProtocolEndpointMismatch = false
	info.FinalRequestRelayFormat = plan.UpstreamRelayFormat
	var body io.Reader
	var closer io.Closer
	if passThrough {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		body = common.NewReplayableBodyReader(storage)
		defer storage.Close()
		if c.Request.ContentLength > 0 {
			info.UpstreamRequestBodySize = c.Request.ContentLength
		}
	} else {
		var requestError *types.NewAPIError
		body, closer, requestError = prepareTextRouteRequest(c, info, request)
		if requestError != nil {
			return nil, requestError
		}
		defer closer.Close()
	}

	upstreamPath, ok := service.BuildTextProtocolPath(plan.UpstreamEndpointType, info.UpstreamModelName, info.IsStream)
	if !ok {
		return nil, types.NewError(fmt.Errorf("unsupported native endpoint type: %s", plan.UpstreamEndpointType), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	plan.UpstreamPath = upstreamPath
	clientPath := info.RequestURLPath
	clientMode := info.RelayMode
	info.RequestURLPath = upstreamPath
	info.RelayMode = plan.UpstreamRelayMode
	respValue, err := adaptor.DoRequest(c, info, body)
	info.RequestURLPath = clientPath
	info.RelayMode = clientMode
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResp, ok := respValue.(*http.Response)
	if !ok || httpResp == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid upstream response type %T", respValue), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	if httpResp.StatusCode != http.StatusOK {
		info.ProtocolEndpointMismatch = httpResp.StatusCode == http.StatusNotFound || httpResp.StatusCode == http.StatusMethodNotAllowed
		newAPIError := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newAPIError, c.GetString("status_code_mapping"))
		return nil, newAPIError
	}
	return HandleNativeTextResponse(c, info, httpResp, plan.ClientRelayFormat, info.IsStream)
}

// prepareTextRouteRequest applies shared channel body controls and creates a replay-safe JSON body.
func prepareTextRouteRequest(c *gin.Context, info *relaycommon.RelayInfo, request any) (io.Reader, io.Closer, *types.NewAPIError) {
	jsonData, apiError := PrepareTextRouteRequestBody(c, info, request)
	if apiError != nil {
		return nil, nil, apiError
	}
	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	info.UpstreamRequestBodySize = int64(len(jsonData))
	return body, closer, nil
}

// PrepareTextRouteRequestBody applies the final-wire controls shared by live relays and semantic channel probes.
func PrepareTextRouteRequestBody(c *gin.Context, info *relaycommon.RelayInfo, request any) ([]byte, *types.NewAPIError) {
	if info == nil {
		return nil, types.NewError(fmt.Errorf("relay info is required"), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	jsonData, err := common.Marshal(request)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeJsonMarshalFailed, types.ErrOptionWithSkipRetry())
	}
	channelOtherSettings := info.ChannelOtherSettings
	jsonData, err = relaycommon.RemoveDisabledFields(jsonData, channelOtherSettings, false)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return nil, newAPIErrorFromParamOverride(err)
		}
	}
	if info.ChannelRoutePlan != nil && info.ChannelRoutePlan.RequestNormalizer != "" {
		normalized, audit, normalizeErr := relaynormalize.NormalizeRequestByIDWithOptions(
			info.ChannelRoutePlan.RequestNormalizer,
			jsonData,
			info.ChannelRoutePlan.NormalizationOptions,
		)
		info.ProtocolNormalization = &audit
		if audit.ReasoningAssistantMessagesPreserved > 0 {
			info.AddReasoningHistoryAudit(
				info.ChannelRoutePlan.UpstreamRelayFormat,
				info.ChannelRoutePlan.UpstreamRelayFormat,
				relaycommon.ReasoningHistoryReasonPreserved,
				audit.ReasoningAssistantMessagesPreserved,
				0, 0, 0,
			)
		}
		if audit.ReasoningOnlyAssistantDropped > 0 {
			info.AddDroppedReasoningOnlyMessages(
				info.ChannelRoutePlan.UpstreamRelayFormat,
				info.ChannelRoutePlan.UpstreamRelayFormat,
				audit.ReasoningOnlyAssistantDropped,
			)
		}
		if audit.ReasoningBlocksDropped > 0 {
			info.AddDroppedReasoningBlocks(
				info.ChannelRoutePlan.UpstreamRelayFormat,
				info.ChannelRoutePlan.UpstreamRelayFormat,
				audit.ReasoningBlocksDropped,
			)
		}
		if normalizeErr != nil {
			return nil, types.NewError(normalizeErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		if validateErr := relaynormalize.ValidateRequestByIDWithOptions(
			info.ChannelRoutePlan.RequestNormalizer,
			normalized,
			info.ChannelRoutePlan.NormalizationOptions,
		); validateErr != nil {
			return nil, types.NewError(validateErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		jsonData = normalized
	}
	logger.LogDebug(c, "planned text request body: %s", jsonData)
	return jsonData, nil
}

func handleConvertedTextResponse(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	upstreamResponse, apiError := decodeProtocolResponse(info.ChannelRoutePlan.UpstreamRelayFormat, body, resp.StatusCode)
	if apiError != nil {
		return nil, apiError
	}
	info.MergeResponseSemantics(info.ChannelRoutePlan.UpstreamRelayFormat, body)
	var resultValue any
	var usage *dto.Usage
	if c.GetBool("legacy_protocol_dto") {
		legacyResponse, convertErr := adaptLegacyResponse(upstreamResponse, info.ChannelRoutePlan.UpstreamRelayFormat)
		if convertErr != nil {
			return nil, types.NewOpenAIError(convertErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		legacyResult, convertErr := legacyconvert.ConvertResponseByID(c, info, info.ChannelRoutePlan.ResponseConverter, legacyResponse)
		if convertErr != nil {
			return nil, types.NewOpenAIError(convertErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		resultValue = legacyResult.Value
		usage = adaptLegacyUsage(legacyResult.Usage)
	} else {
		result, convertErr := relayconvert.ConvertResponseByID(c, info, info.ChannelRoutePlan.ResponseConverter, upstreamResponse)
		if convertErr != nil {
			return nil, types.NewOpenAIError(convertErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		resultValue = result.Value
		usage = result.Usage
	}
	responseBody, err := common.Marshal(resultValue)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	service.IOCopyBytesGracefully(c, resp, responseBody)
	return ensureConvertedUsage(c, info, usage, protocolResponseUsageText(resultValue)), nil
}

func adaptLegacyResponse(response any, format types.RelayFormat) (any, error) {
	var target any
	switch format {
	case types.RelayFormatOpenAI:
		target = &legacydto.OpenAITextResponse{}
	case types.RelayFormatOpenAIResponses:
		target = &legacydto.OpenAIResponsesResponse{}
	case types.RelayFormatClaude:
		target = &legacydto.ClaudeResponse{}
	case types.RelayFormatGemini:
		target = &legacydto.GeminiChatResponse{}
	default:
		return response, nil
	}
	data, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	if err := common.Unmarshal(data, target); err != nil {
		return nil, err
	}
	return target, nil
}

func adaptLegacyUsage(usage *legacydto.Usage) *dto.Usage {
	if usage == nil {
		return nil
	}
	data, err := common.Marshal(usage)
	if err != nil {
		return nil
	}
	var adapted dto.Usage
	if err := common.Unmarshal(data, &adapted); err != nil {
		return nil
	}
	return &adapted
}

func handleConvertedTextStream(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	plan := info.ChannelRoutePlan
	state, err := relayconvert.NewResponseStreamStateByID(plan.ResponseConverter, relayconvert.ResponseStreamOptions{
		ID:           helper.GetResponseID(c),
		Model:        info.UpstreamModelName,
		Created:      time.Now().Unix(),
		IncludeUsage: info.ShouldIncludeUsage,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	var streamErr *types.NewAPIError
	var usageText strings.Builder
	helpersWrite := func(result relayconvert.ResponseResult) bool {
		if err := writeConvertedStreamResult(c, info, plan.ClientRelayFormat, result.Value); err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
		return true
	}

	scannerOptions := helper.StreamScannerOptions{
		RequireExplicitTerminal: plan.UpstreamRelayFormat == types.RelayFormatOpenAIResponses ||
			plan.UpstreamRelayFormat == types.RelayFormatOpenAI,
	}
	helper.StreamScannerHandlerWithOptions(c, resp, info, scannerOptions, func(data string, sr *helper.StreamResult) {
		if streamErr != nil {
			sr.Stop(streamErr)
			return
		}
		chunk, decodeErr := decodeProtocolStreamChunk(plan.UpstreamRelayFormat, data)
		if decodeErr != nil {
			streamErr = types.NewOpenAIError(decodeErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
			if responseEvent, ok := chunk.(*dto.ResponsesStreamResponse); ok {
				if terminalStatus, terminal := responsesStreamTerminal(responseEvent); terminal {
					sr.StopWithTerminal(responseEvent.Type, terminalStatus, streamErr)
					return
				}
			}
			sr.Stop(streamErr)
			return
		}
		usageText.WriteString(protocolStreamChunkUsageText(chunk))
		results, convertErr := relayconvert.ConvertStreamResponseChunk(c, info, state, chunk)
		if convertErr != nil {
			streamErr = types.NewOpenAIError(convertErr, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, result := range results {
			if !helpersWrite(result) {
				sr.Stop(streamErr)
				return
			}
		}
		if responseEvent, ok := chunk.(*dto.ResponsesStreamResponse); ok {
			if terminalStatus, terminal := responsesStreamTerminal(responseEvent); terminal {
				sr.DoneWithTerminal(responseEvent.Type, terminalStatus)
			}
		}
		if chatChunk, ok := chunk.(*dto.ChatCompletionsStreamResponse); ok {
			markChatStreamTerminal(sr, chatChunk)
		}
	})
	if streamErr != nil {
		observePartialProtocolStreamUsage(c, info, state, usageText.String())
		return nil, streamErr
	}
	if statusErr := helper.StreamStatusError(c, info); statusErr != nil {
		observePartialProtocolStreamUsage(c, info, state, usageText.String())
		return nil, statusErr
	}

	text := usageText.String()
	if text == "" {
		text = state.UsageText()
	}
	usage := ensureConvertedUsage(c, info, state.Usage(), text)
	state.SetUsage(usage)
	finalResults, err := relayconvert.FinalizeStreamResponse(c, info, state)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	for _, result := range finalResults {
		if !helpersWrite(result) {
			return nil, streamErr
		}
	}
	if plan.ClientRelayFormat == types.RelayFormatOpenAI {
		helper.Done(c)
	}
	return usage, nil
}

// HandleNativeTextResponse validates and records an upstream response without protocol conversion.
func HandleNativeTextResponse(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, format types.RelayFormat, stream bool) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid upstream response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	if !stream {
		defer service.CloseResponseBodyGracefully(resp)
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
		}
		decoded, apiError := decodeProtocolResponse(format, body, resp.StatusCode)
		if apiError != nil {
			return nil, apiError
		}
		if err := validateNativeTextResponse(format, decoded); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		info.MergeResponseSemantics(format, body)
		result, err := relayconvert.ConvertResponse(c, info, format, decoded)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		service.IOCopyBytesGracefully(c, resp, body)
		return ensureConvertedUsage(c, info, result.Usage, protocolResponseUsageText(decoded)), nil
	}

	state, err := relayconvert.NewResponseStreamState(format, format, relayconvert.ResponseStreamOptions{
		ID:           helper.GetResponseID(c),
		Model:        info.UpstreamModelName,
		Created:      time.Now().Unix(),
		IncludeUsage: info.ShouldIncludeUsage,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	var streamErr *types.NewAPIError
	var usageText strings.Builder
	scannerOptions := helper.StreamScannerOptions{
		RequireExplicitTerminal: format == types.RelayFormatOpenAIResponses || format == types.RelayFormatOpenAI,
	}
	helper.StreamScannerHandlerWithOptions(c, resp, info, scannerOptions, func(data string, sr *helper.StreamResult) {
		chunk, decodeErr := decodeProtocolStreamChunk(format, data)
		if decodeErr != nil {
			streamErr = types.NewOpenAIError(decodeErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
			if responseEvent, ok := chunk.(*dto.ResponsesStreamResponse); ok {
				if terminalStatus, terminal := responsesStreamTerminal(responseEvent); terminal {
					sr.StopWithTerminal(responseEvent.Type, terminalStatus, streamErr)
					return
				}
			}
			sr.Stop(streamErr)
			return
		}
		usageText.WriteString(protocolStreamChunkUsageText(chunk))
		if validateErr := validateNativeTextStreamChunk(format, chunk); validateErr != nil {
			streamErr = types.NewOpenAIError(validateErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		results, convertErr := relayconvert.ConvertStreamResponseChunk(c, info, state, chunk)
		if convertErr != nil {
			streamErr = types.NewOpenAIError(convertErr, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, result := range results {
			if writeErr := writeConvertedStreamResult(c, info, format, result.Value); writeErr != nil {
				streamErr = types.NewOpenAIError(writeErr, types.ErrorCodeBadResponse, http.StatusInternalServerError)
				sr.Stop(streamErr)
				return
			}
		}
		if responseEvent, ok := chunk.(*dto.ResponsesStreamResponse); ok {
			if terminalStatus, terminal := responsesStreamTerminal(responseEvent); terminal {
				sr.DoneWithTerminal(responseEvent.Type, terminalStatus)
			}
		}
		if chatChunk, ok := chunk.(*dto.ChatCompletionsStreamResponse); ok {
			markChatStreamTerminal(sr, chatChunk)
		}
	})
	if streamErr != nil {
		observePartialProtocolStreamUsage(c, info, state, usageText.String())
		return nil, streamErr
	}
	if statusErr := helper.StreamStatusError(c, info); statusErr != nil {
		observePartialProtocolStreamUsage(c, info, state, usageText.String())
		return nil, statusErr
	}
	if format == types.RelayFormatOpenAI {
		helper.Done(c)
	}
	text := usageText.String()
	if text == "" {
		text = state.UsageText()
	}
	return ensureConvertedUsage(c, info, state.Usage(), text), nil
}

// markChatStreamTerminal records a semantic Chat Completions terminal without
// stopping the scanner, because a usage-only chunk or [DONE] may follow it.
func markChatStreamTerminal(sr *helper.StreamResult, chunk *dto.ChatCompletionsStreamResponse) {
	if sr == nil || chunk == nil {
		return
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason == nil || strings.TrimSpace(*choice.FinishReason) == "" {
			continue
		}
		terminalStatus, _ := relayconvert.ResponsesStatusFromChatFinishReason(*choice.FinishReason)
		if terminalStatus == "" {
			terminalStatus = "completed"
		}
		sr.MarkTerminal("chat.finish_reason", terminalStatus)
		return
	}
}

func validateNativeTextResponse(format types.RelayFormat, response any) error {
	switch format {
	case types.RelayFormatOpenAI:
		value, ok := response.(*dto.OpenAITextResponse)
		if !ok || value == nil || (value.Id == "" && value.Object == "" && len(value.Choices) == 0) {
			return fmt.Errorf("invalid chat response shape")
		}
	case types.RelayFormatOpenAIResponses:
		value, ok := response.(*dto.OpenAIResponsesResponse)
		if !ok || value == nil || (value.ID == "" && value.Object == "" && len(value.Status) == 0) {
			return fmt.Errorf("invalid responses response shape")
		}
	case types.RelayFormatClaude:
		value, ok := response.(*dto.ClaudeResponse)
		if !ok || value == nil || value.Type == "" {
			return fmt.Errorf("invalid messages response shape")
		}
	case types.RelayFormatGemini:
		value, ok := response.(*dto.GeminiChatResponse)
		if !ok || value == nil || (len(value.Candidates) == 0 && value.PromptFeedback == nil && !value.HasUsageMetadata) {
			return fmt.Errorf("invalid generate content response shape")
		}
	default:
		return fmt.Errorf("unsupported native response format: %s", format)
	}
	return nil
}

func validateNativeTextStreamChunk(format types.RelayFormat, chunk any) error {
	switch format {
	case types.RelayFormatOpenAI:
		value, ok := chunk.(*dto.ChatCompletionsStreamResponse)
		if (!ok || value == nil || (value.Id == "" && value.Object == "" && len(value.Choices) == 0 && value.Usage == nil)) && !validLegacyChatStreamChunk(chunk) {
			return fmt.Errorf("invalid chat stream shape")
		}
	case types.RelayFormatOpenAIResponses:
		value, ok := chunk.(*dto.ResponsesStreamResponse)
		if (!ok || value == nil || value.Type == "") && !validLegacyResponsesStreamChunk(chunk) {
			return fmt.Errorf("invalid responses stream shape")
		}
	case types.RelayFormatClaude:
		value, ok := chunk.(*dto.ClaudeResponse)
		if (!ok || value == nil || value.Type == "") && !validLegacyClaudeStreamChunk(chunk) {
			return fmt.Errorf("invalid messages stream shape")
		}
	case types.RelayFormatGemini:
		value, ok := chunk.(*dto.GeminiChatResponse)
		if (!ok || value == nil || (len(value.Candidates) == 0 && value.PromptFeedback == nil && !value.HasUsageMetadata)) && !validLegacyGeminiStreamChunk(chunk) {
			return fmt.Errorf("invalid generate content stream shape")
		}
	default:
		return fmt.Errorf("unsupported native stream format: %s", format)
	}
	return nil
}

func validLegacyChatStreamChunk(chunk any) bool {
	value, ok := chunk.(*legacydto.ChatCompletionsStreamResponse)
	return ok && value != nil && (value.Id != "" || value.Object != "" || len(value.Choices) > 0 || value.Usage != nil)
}

func validLegacyResponsesStreamChunk(chunk any) bool {
	value, ok := chunk.(*legacydto.ResponsesStreamResponse)
	return ok && value != nil && value.Type != ""
}

func validLegacyClaudeStreamChunk(chunk any) bool {
	value, ok := chunk.(*legacydto.ClaudeResponse)
	return ok && value != nil && value.Type != ""
}

func validLegacyGeminiStreamChunk(chunk any) bool {
	value, ok := chunk.(*legacydto.GeminiChatResponse)
	return ok && value != nil && (len(value.Candidates) > 0 || value.PromptFeedback != nil || value.HasUsageMetadata)
}

func decodeProtocolResponse(format types.RelayFormat, data []byte, statusCode int) (any, *types.NewAPIError) {
	if payloadErr := decodeProtocolPayloadError(data); payloadErr != nil {
		errorStatus := statusCode
		if errorStatus < http.StatusBadRequest {
			errorStatus = http.StatusBadGateway
		}
		return nil, types.NewOpenAIError(payloadErr, types.ErrorCodeBadResponseBody, errorStatus)
	}
	switch format {
	case types.RelayFormatOpenAI:
		var response dto.OpenAITextResponse
		if err := common.Unmarshal(data, &response); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if openAIError := response.GetOpenAIError(); openAIError != nil && openAIError.Type != "" {
			return nil, types.WithOpenAIError(*openAIError, statusCode)
		}
		return &response, nil
	case types.RelayFormatOpenAIResponses:
		var response dto.OpenAIResponsesResponse
		if err := common.Unmarshal(data, &response); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if openAIError := response.GetOpenAIError(); openAIError != nil && openAIError.Type != "" {
			return nil, types.WithOpenAIError(*openAIError, statusCode)
		}
		return &response, nil
	case types.RelayFormatClaude:
		var response dto.ClaudeResponse
		if err := common.Unmarshal(data, &response); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if claudeError := response.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
			return nil, types.WithClaudeError(*claudeError, statusCode)
		}
		return &response, nil
	case types.RelayFormatGemini:
		var response dto.GeminiChatResponse
		if err := common.Unmarshal(data, &response); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		return &response, nil
	default:
		return nil, types.NewOpenAIError(fmt.Errorf("unsupported upstream response format: %s", format), types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
}

func decodeProtocolStreamChunk(format types.RelayFormat, data string) (any, error) {
	if format != types.RelayFormatOpenAIResponses {
		if payloadErr := decodeProtocolPayloadError([]byte(data)); payloadErr != nil {
			return nil, payloadErr
		}
	}
	switch format {
	case types.RelayFormatOpenAIResponses:
		var response dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &response); err != nil {
			return nil, err
		}
		if terminalStatus, terminal := responsesStreamTerminal(&response); terminal && terminalStatus == "failed" {
			if openAIError := response.GetOpenAIError(); openAIError != nil && strings.TrimSpace(openAIError.Message) != "" {
				return &response, fmt.Errorf("responses stream error: %s", openAIError.Message)
			}
			return &response, fmt.Errorf("responses stream error: %s", response.Type)
		}
		return &response, nil
	}
	switch format {
	case types.RelayFormatOpenAI:
		var response dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &response); err != nil {
			return nil, err
		}
		return &response, nil
	case types.RelayFormatClaude:
		var response dto.ClaudeResponse
		if err := common.UnmarshalJsonStr(data, &response); err != nil {
			return nil, err
		}
		if claudeError := response.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
			return nil, fmt.Errorf("messages stream error: %s", claudeError.Message)
		}
		return &response, nil
	case types.RelayFormatGemini:
		var response dto.GeminiChatResponse
		if err := common.UnmarshalJsonStr(data, &response); err != nil {
			return nil, err
		}
		return &response, nil
	default:
		return nil, fmt.Errorf("unsupported upstream stream format: %s", format)
	}
}

// responsesStreamTerminal classifies OpenAI Responses terminal events.
func responsesStreamTerminal(response *dto.ResponsesStreamResponse) (string, bool) {
	if response == nil {
		return "", false
	}
	switch response.Type {
	case "response.completed", "response.done":
		return "completed", true
	case "response.incomplete":
		return "incomplete", true
	case "error", "response.error", "response.failed", "response.cancelled":
		return "failed", true
	default:
		return "", false
	}
}

func decodeProtocolPayloadError(data []byte) error {
	var envelope struct {
		Type    string          `json:"type"`
		Error   json.RawMessage `json:"error"`
		Message json.RawMessage `json:"message"`
	}
	if err := common.Unmarshal(data, &envelope); err != nil {
		return nil
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" && string(envelope.Error) != "{}" {
		var nested struct {
			Message string `json:"message"`
		}
		if err := common.Unmarshal(envelope.Error, &nested); err == nil && strings.TrimSpace(nested.Message) != "" {
			return fmt.Errorf("upstream protocol error: %s", nested.Message)
		}
		var message string
		if err := common.Unmarshal(envelope.Error, &message); err == nil && strings.TrimSpace(message) != "" {
			return fmt.Errorf("upstream protocol error: %s", message)
		}
		return fmt.Errorf("upstream protocol error")
	}
	payloadType := strings.ToLower(strings.TrimSpace(envelope.Type))
	if payloadType == "error" || payloadType == "upstream_error" || payloadType == "response.error" || payloadType == "response.failed" {
		var message string
		if err := common.Unmarshal(envelope.Message, &message); err == nil && strings.TrimSpace(message) != "" {
			return fmt.Errorf("upstream protocol error: %s", message)
		}
		return fmt.Errorf("upstream protocol error: %s", payloadType)
	}
	return nil
}

// writeConvertedStreamResult writes one downstream event and records only payloads that were flushed successfully.
func writeConvertedStreamResult(c *gin.Context, info *relaycommon.RelayInfo, clientFormat types.RelayFormat, value any) error {
	var payload []byte
	var err error
	switch clientFormat {
	case types.RelayFormatOpenAI:
		payload, err = common.Marshal(value)
		if err == nil {
			err = helper.StringData(c, string(payload))
		}
	case types.RelayFormatOpenAIResponses:
		switch event := value.(type) {
		case relayconvert.ChatToResponsesStreamEvent:
			payload, err = common.Marshal(event.Payload)
			if err == nil {
				err = helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, string(payload))
			}
		case *relayconvert.ChatToResponsesStreamEvent:
			if event == nil {
				return nil
			}
			payload, err = common.Marshal(event.Payload)
			if err == nil {
				err = helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, string(payload))
			}
		default:
			payload, err = common.Marshal(value)
			if err == nil {
				var responseEvent dto.ResponsesStreamResponse
				if err = common.Unmarshal(payload, &responseEvent); err == nil {
					err = helper.ResponseChunkData(c, responseEvent, string(payload))
				}
			}
		}
	case types.RelayFormatClaude:
		switch response := value.(type) {
		case dto.ClaudeResponse:
			payload, err = common.Marshal(response)
			if err == nil {
				err = helper.ClaudeData(c, response)
			}
		case *dto.ClaudeResponse:
			if response == nil {
				return nil
			}
			payload, err = common.Marshal(response)
			if err == nil {
				err = helper.ClaudeData(c, *response)
			}
		default:
			return fmt.Errorf("unexpected messages stream response type %T", value)
		}
	case types.RelayFormatGemini:
		payload, err = common.Marshal(value)
		if err == nil {
			err = helper.StringData(c, string(payload))
		}
	default:
		return fmt.Errorf("unsupported client stream format: %s", clientFormat)
	}
	if err != nil {
		return err
	}
	if info == nil || info.StreamStatus == nil {
		return nil
	}
	observation := helper.ObserveStreamDataPayload(string(payload), clientFormat)
	if observation.Meaningful {
		info.StreamStatus.MarkClientPayloadCommitted()
	}
	info.StreamStatus.ObserveToolPayloadBytes(observation.ToolNameBytes, observation.ToolArgumentBytes)
	return nil
}

// observePartialProtocolStreamUsage records the best available usage after an interrupted decoded stream.
func observePartialProtocolStreamUsage(c *gin.Context, info *relaycommon.RelayInfo, state *relayconvert.ResponseStreamState, text string) *dto.Usage {
	if info == nil || state == nil {
		return nil
	}
	upstreamUsage := state.Usage()
	usage := ensureConvertedUsage(c, info, upstreamUsage, text)
	if info.StreamStatus != nil {
		hasUpstreamUsage := upstreamUsage != nil && upstreamUsage.TotalTokens > 0
		info.StreamStatus.ObservePartialUsage(usage, hasUpstreamUsage, !hasUpstreamUsage)
	}
	return usage
}

func protocolResponseUsageText(response any) string {
	var text strings.Builder
	switch value := response.(type) {
	case *dto.OpenAITextResponse:
		for i := range value.Choices {
			message := &value.Choices[i].Message
			text.WriteString(message.StringContent())
			text.WriteString(message.GetReasoningContent())
			for _, toolCall := range message.ParseToolCalls() {
				text.WriteString(toolCall.Function.Arguments)
			}
		}
	case *dto.OpenAIResponsesResponse:
		for i := range value.Output {
			output := &value.Output[i]
			for _, content := range output.Content {
				text.WriteString(content.Text)
			}
			text.WriteString(common.JsonRawMessageToString(output.Arguments))
		}
	case *dto.ClaudeResponse:
		text.WriteString(value.Completion)
		for i := range value.Content {
			content := &value.Content[i]
			text.WriteString(content.GetText())
			if content.Thinking != nil {
				text.WriteString(*content.Thinking)
			}
			if content.Input != nil {
				appendProtocolUsageJSON(&text, content.Input)
			}
		}
	case *dto.GeminiChatResponse:
		for _, candidate := range value.Candidates {
			for _, part := range candidate.Content.Parts {
				text.WriteString(part.Text)
				if part.FunctionCall != nil {
					text.WriteString(part.FunctionCall.FunctionName)
					appendProtocolUsageJSON(&text, part.FunctionCall.Arguments)
				}
			}
		}
	case *legacydto.OpenAITextResponse:
		for i := range value.Choices {
			message := &value.Choices[i].Message
			text.WriteString(message.StringContent())
			text.WriteString(message.GetReasoningContent())
			for _, toolCall := range message.ParseToolCalls() {
				text.WriteString(toolCall.Function.Arguments)
			}
		}
	case *legacydto.OpenAIResponsesResponse:
		for i := range value.Output {
			output := &value.Output[i]
			for _, content := range output.Content {
				text.WriteString(content.Text)
			}
			text.WriteString(common.JsonRawMessageToString(output.Arguments))
		}
	case *legacydto.ClaudeResponse:
		text.WriteString(value.Completion)
		for i := range value.Content {
			content := &value.Content[i]
			text.WriteString(content.GetText())
			if content.Thinking != nil {
				text.WriteString(*content.Thinking)
			}
			if content.Input != nil {
				appendProtocolUsageJSON(&text, content.Input)
			}
		}
	case *legacydto.GeminiChatResponse:
		for _, candidate := range value.Candidates {
			for _, part := range candidate.Content.Parts {
				text.WriteString(part.Text)
				if part.FunctionCall != nil {
					text.WriteString(part.FunctionCall.FunctionName)
					appendProtocolUsageJSON(&text, part.FunctionCall.Arguments)
				}
			}
		}
	}
	return text.String()
}

func protocolStreamChunkUsageText(chunk any) string {
	var text strings.Builder
	switch value := chunk.(type) {
	case *dto.ChatCompletionsStreamResponse:
		for _, choice := range value.Choices {
			text.WriteString(choice.Delta.GetContentString())
			text.WriteString(choice.Delta.GetReasoningContent())
			for _, toolCall := range choice.Delta.ToolCalls {
				text.WriteString(toolCall.Function.Arguments)
			}
		}
	case *dto.ResponsesStreamResponse:
		text.WriteString(value.Delta)
	case *dto.ClaudeResponse:
		for _, content := range []*dto.ClaudeMediaMessage{value.ContentBlock, value.Delta} {
			if content == nil {
				continue
			}
			text.WriteString(content.GetText())
			text.WriteString(content.Delta)
			if content.Thinking != nil {
				text.WriteString(*content.Thinking)
			}
			if content.PartialJson != nil {
				text.WriteString(*content.PartialJson)
			}
		}
	case *dto.GeminiChatResponse:
		for _, candidate := range value.Candidates {
			for _, part := range candidate.Content.Parts {
				text.WriteString(part.Text)
				if part.FunctionCall != nil {
					text.WriteString(part.FunctionCall.FunctionName)
					appendProtocolUsageJSON(&text, part.FunctionCall.Arguments)
				}
			}
		}
	case *legacydto.ChatCompletionsStreamResponse:
		for _, choice := range value.Choices {
			text.WriteString(choice.Delta.GetContentString())
			text.WriteString(choice.Delta.GetReasoningContent())
			for _, toolCall := range choice.Delta.ToolCalls {
				text.WriteString(toolCall.Function.Arguments)
			}
		}
	case *legacydto.ResponsesStreamResponse:
		text.WriteString(value.Delta)
	case *legacydto.ClaudeResponse:
		for _, content := range []*legacydto.ClaudeMediaMessage{value.ContentBlock, value.Delta} {
			if content == nil {
				continue
			}
			text.WriteString(content.GetText())
			text.WriteString(content.Delta)
			if content.Thinking != nil {
				text.WriteString(*content.Thinking)
			}
			if content.PartialJson != nil {
				text.WriteString(*content.PartialJson)
			}
		}
	case *legacydto.GeminiChatResponse:
		for _, candidate := range value.Candidates {
			for _, part := range candidate.Content.Parts {
				text.WriteString(part.Text)
				if part.FunctionCall != nil {
					text.WriteString(part.FunctionCall.FunctionName)
					appendProtocolUsageJSON(&text, part.FunctionCall.Arguments)
				}
			}
		}
	}
	return text.String()
}

func appendProtocolUsageJSON(text *strings.Builder, value any) {
	data, err := common.Marshal(value)
	if err == nil {
		text.Write(data)
	}
}

func ensureConvertedUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, text string) *dto.Usage {
	if usage != nil && usage.TotalTokens > 0 {
		return usage
	}
	return service.ResponseText2Usage(c, text, info.UpstreamModelName, info.GetEstimatePromptTokens())
}

// textRouteAllowsPassThrough keeps client bytes only when no wire conversion is required.
func textRouteAllowsPassThrough(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	if isConvertedProtocolRoute(info) || isNormalizedProtocolRoute(info) {
		return false
	}
	if isUnconvertedProtocolRoute(info) {
		return true
	}
	// Fixed-protocol adaptors cannot accept another protocol's original JSON body.
	switch info.ApiType {
	case constant.APITypeAnthropic:
		return info.RelayFormat == types.RelayFormatClaude
	case constant.APITypeGemini:
		return info.RelayFormat == types.RelayFormatGemini
	default:
		return true
	}
}
