package claude

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// ClaudeResponsesStreamHandler finalizes the converted response only after an upstream message_stop.
func ClaudeResponsesStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	responseID := helper.GetResponseID(c)
	created := common.GetTimestamp()
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatClaude, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID:                 responseID,
		Model:              info.UpstreamModelName,
		Created:            created,
		EmitSequenceNumber: true,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	hostedBridge := relayconvert.NewClaudeHostedStreamBridge()

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   responseID,
		Created:      created,
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var streamErr *types.NewAPIError
	sendResponsesEvent := func(eventType string, payload dto.ResponsesStreamResponse) bool {
		payload.Type = eventType
		if err := helper.WriteConvertedResponsesEvent(c, info, payload); err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
		return true
	}
	sendResult := func(result relayconvert.ResponseResult) bool {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			streamErr = types.NewOpenAIError(
				fmt.Errorf("expected OpenAI Responses stream event, got %T", result.Value),
				types.ErrorCodeBadResponse,
				http.StatusInternalServerError,
			)
			return false
		}
		return sendResponsesEvent(event.Type, event.Payload)
	}
	// Preserve the native Responses error sequence; the controller handles settlement, not another frame.
	failResponsesStream := func(err *types.NewAPIError) *types.NewAPIError {
		failureResults, _ := state.FailResponsesStream("server_error", err.Error(), "")
		for _, result := range failureResults {
			if !sendResult(result) {
				err = streamErr
				break
			}
		}
		if info.StreamStatus.ErrorFrameIsWritten() {
			return types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithClientErrorWritten())
		}
		return err
	}

	if rejection := helper.ScanTextStream(c, resp, info, helper.StreamScannerOptions{
		RequireExplicitTerminal: true,
		RequiredTerminalEvent:   "message_stop",
	}, func(data string, sr *helper.StreamResult) {
		var claudeResponse dto.ClaudeResponse
		if err := common.UnmarshalJsonStr(data, &claudeResponse); err != nil {
			logger.LogError(c, common.LogText("failed to unmarshal Claude stream event: %s", err.Error()))
			streamErr = failResponsesStream(types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway))
			sr.Stop(streamErr)
			return
		}
		if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
			streamErr = failResponsesStream(types.WithClaudeError(*claudeError, http.StatusInternalServerError))
			sr.Stop(streamErr)
			return
		}

		if claudeResponse.StopReason != "" {
			maybeMarkClaudeRefusal(c, info, claudeResponse.StopReason)
		}
		if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
			maybeMarkClaudeRefusal(c, info, *claudeResponse.Delta.StopReason)
		}
		if claudeResponse.Type == "message_start" && claudeResponse.Message != nil {
			info.ObserveResponseModel(claudeResponse.Message.Model)
			info.UpstreamModelName = claudeResponse.Message.Model
		}
		FormatClaudeResponseInfo(&claudeResponse, nil, claudeInfo)
		if claudeResponse.Usage != nil || (claudeResponse.Message != nil && claudeResponse.Message.Usage != nil) {
			info.StreamStatus.ObservePartialUsage(claudeInfo.Usage, true, false)
		}
		countClaudeStreamBillableTools(info, &claudeResponse)
		hostedEvents, consumed, err := hostedBridge.Convert(&claudeResponse, state)
		if err != nil {
			streamErr = failResponsesStream(types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError))
			sr.Stop(streamErr)
			return
		}
		for _, event := range hostedEvents {
			if !sendResponsesEvent(event.Type, event.Payload) {
				sr.Stop(streamErr)
				return
			}
		}
		if consumed {
			return
		}

		results, err := service.ConvertStreamResponseChunk(c, info, state, &claudeResponse)
		if err != nil {
			streamErr = failResponsesStream(types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError))
			sr.Stop(streamErr)
			return
		}
		for _, result := range results {
			if !sendResult(result) {
				sr.Stop(streamErr)
				return
			}
		}
		if claudeResponse.Type == "message_stop" {
			sr.DoneWithTerminal(claudeResponse.Type, "completed")
		}
	}); rejection != nil {
		return nil, rejection
	}
	if streamErr == nil {
		streamErr = helper.StreamStatusError(c, info)
	}
	HandleStreamFinalResponse(c, info, claudeInfo)
	if streamErr != nil {
		info.StreamStatus.ObservePartialUsage(claudeInfo.Usage, false, true)
		return claudeInfo.Usage, streamErr
	}
	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
	state.SetUsage(&openAIUsage)
	finalResults, err := service.FinalizeStreamResponse(c, info, state)
	if err != nil {
		return claudeInfo.Usage, failResponsesStream(types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError))
	}
	for _, result := range finalResults {
		if !sendResult(result) {
			return nil, streamErr
		}
	}
	return claudeInfo.Usage, nil
}
