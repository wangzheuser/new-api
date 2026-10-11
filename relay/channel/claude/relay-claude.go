package claude

import (
	"cmp"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

func stopReasonClaude2OpenAI(reason string) string {
	return relayconvert.StopReasonClaudeToOpenAI(reason)
}

func maybeMarkClaudeRefusal(c *gin.Context, info *relaycommon.RelayInfo, stopReason string) {
	if c == nil {
		return
	}
	if strings.EqualFold(stopReason, "refusal") {
		info.PerformanceBusinessRejection = true
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "claude_stop_reason=refusal")
	}
}

func StreamResponseClaude2OpenAI(claudeResponse *dto.ClaudeResponse) *dto.ChatCompletionsStreamResponse {
	return relayconvert.StreamResponseClaude2OpenAI(claudeResponse)
}

func ResponseClaude2OpenAI(claudeResponse *dto.ClaudeResponse) *dto.OpenAITextResponse {
	return relayconvert.ResponseClaude2OpenAI(claudeResponse)
}

type ClaudeResponseInfo = relayconvert.ClaudeResponseInfo

func cacheCreationTokensForOpenAIUsage(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	openAIUsage := relayconvert.UsageFromClaudeUsage(usage)
	if openAIUsage == nil {
		return 0
	}
	return openAIUsage.PromptTokens - usage.PromptTokens - usage.PromptTokensDetails.CachedTokens
}

func buildOpenAIStyleUsageFromClaudeUsage(usage *dto.Usage) dto.Usage {
	mapped := relayconvert.UsageFromClaudeUsage(usage)
	if mapped == nil {
		return dto.Usage{}
	}
	return *mapped
}

func buildMessageDeltaPatchUsage(claudeResponse *dto.ClaudeResponse, claudeInfo *ClaudeResponseInfo) *dto.ClaudeUsage {
	return relayconvert.BuildMessageDeltaPatchUsage(claudeResponse, claudeInfo)
}

func shouldSkipClaudeMessageDeltaUsagePatch(info *relaycommon.RelayInfo) bool {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		return true
	}
	if info == nil {
		return false
	}
	return info.ChannelSetting.PassThroughBodyEnabled
}

func patchClaudeMessageDeltaUsageData(data string, usage *dto.ClaudeUsage) string {
	return relayconvert.PatchClaudeMessageDeltaUsageData(data, usage)
}

func FormatClaudeResponseInfo(claudeResponse *dto.ClaudeResponse, oaiResponse *dto.ChatCompletionsStreamResponse, claudeInfo *ClaudeResponseInfo) bool {
	return relayconvert.FormatClaudeResponseInfo(claudeResponse, oaiResponse, claudeInfo)
}

func HandleStreamResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, data string, conversion ...*relayconvert.ResponseStreamState) *types.NewAPIError {
	var claudeResponse dto.ClaudeResponse
	err := common.UnmarshalJsonStr(data, &claudeResponse)
	if err != nil {
		common.SysLog(common.LogText("error unmarshalling stream response: %s", err.Error()))
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	if claudeResponse.Type == "message_start" && claudeResponse.Message != nil {
		info.ObserveResponseModel(claudeResponse.Message.Model)
	}
	if claudeResponse.StopReason != "" {
		maybeMarkClaudeRefusal(c, info, claudeResponse.StopReason)
	}
	if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
		maybeMarkClaudeRefusal(c, info, *claudeResponse.Delta.StopReason)
	}
	if claudeResponse.Type == "message_stop" {
		info.StreamStatus.MarkCompleted()
	}
	if info.RelayFormat == types.RelayFormatClaude {
		if claudeResponse.Type == "message_start" {
			// message_start, 获取usage
			if claudeResponse.Message != nil {
				info.UpstreamModelName = claudeResponse.Message.Model
			}
		} else if claudeResponse.Type == "message_delta" {
			// 确保 message_delta 的 usage 包含完整的 input_tokens 和 cache 相关字段
			// 解决 AWS Bedrock 等上游返回的 message_delta 缺少这些字段的问题
			if !shouldSkipClaudeMessageDeltaUsagePatch(info) {
				data = patchClaudeMessageDeltaUsageData(data, buildMessageDeltaPatchUsage(&claudeResponse, claudeInfo))
			}
		}
		if err = helper.ClaudeChunkData(c, claudeResponse, data); err != nil {
			return types.NewError(err, types.ErrorCodeBadResponse)
		}
		FormatClaudeResponseInfo(&claudeResponse, nil, claudeInfo)
		if info.StreamStatus != nil {
			observation := helper.ObserveStreamDataPayload(data, info.RelayFormat)
			if observation.Meaningful {
				info.StreamStatus.MarkClientPayloadCommitted()
			}
			info.StreamStatus.ObserveToolPayloadBytes(
				observation.ToolNameBytes,
				observation.ToolArgumentBytes,
			)
		}
	} else if info.RelayFormat == types.RelayFormatOpenAI {
		state, err := claudeToChatStreamState(info)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		response, err := state.ConvertChunk(&claudeResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}

		if !FormatClaudeResponseInfo(&claudeResponse, response, claudeInfo) {
			return nil
		}

		countClaudeStreamBillableTools(info, &claudeResponse)

		if response == nil {
			return nil
		}
		err = helper.ObjectData(c, response)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponse)
		}
		if info.StreamStatus != nil {
			responseData, marshalErr := common.Marshal(response)
			if marshalErr != nil {
				return types.NewError(marshalErr, types.ErrorCodeBadResponseBody)
			}
			observation := helper.ObserveStreamDataPayload(string(responseData), info.RelayFormat)
			if observation.Meaningful {
				info.StreamStatus.MarkClientPayloadCommitted()
			}
			info.StreamStatus.ObserveToolPayloadBytes(
				observation.ToolNameBytes,
				observation.ToolArgumentBytes,
			)
		}
	}
	if len(conversion) > 0 && conversion[0] != nil {
		response := StreamResponseClaude2OpenAI(&claudeResponse)
		if FormatClaudeResponseInfo(&claudeResponse, response, claudeInfo) {
			results, convertErr := relayconvert.ConvertStreamResponseChunk(c, info, conversion[0], response)
			if convertErr != nil {
				return types.NewError(convertErr, types.ErrorCodeBadResponse)
			}
			if sendErr := writeClaudeConvertedEvents(c, info, results); sendErr != nil {
				return sendErr
			}
		}
	}
	if info.StreamStatus != nil && claudeInfo.Usage != nil {
		info.StreamStatus.ObservePartialUsage(claudeInfo.Usage, claudeResponse.Usage != nil, claudeResponse.Usage == nil)
	}
	return nil
}

func claudeToChatStreamState(info *relaycommon.RelayInfo) (*relayconvert.ClaudeToChatStreamState, error) {
	if info != nil && info.ClaudeToChatStreamState != nil {
		state, ok := info.ClaudeToChatStreamState.(*relayconvert.ClaudeToChatStreamState)
		if !ok || state == nil {
			return nil, fmt.Errorf("invalid Claude-to-Chat stream state %T", info.ClaudeToChatStreamState)
		}
		return state, nil
	}

	state := relayconvert.NewClaudeToChatStreamState()
	if info != nil {
		info.ClaudeToChatStreamState = state
	}
	return state, nil
}

func claudeToGeminiStreamState(info *relaycommon.RelayInfo) (*relayconvert.ResponseStreamState, error) {
	if info != nil && info.ChatToGeminiStreamState != nil {
		state, ok := info.ChatToGeminiStreamState.(*relayconvert.ResponseStreamState)
		if !ok || state == nil {
			return nil, fmt.Errorf("invalid Claude-to-Gemini stream state %T", info.ChatToGeminiStreamState)
		}
		return state, nil
	}

	state, err := relayconvert.NewResponseStreamState(types.RelayFormatClaude, types.RelayFormatGemini, relayconvert.ResponseStreamOptions{})
	if err != nil {
		return nil, err
	}
	if info != nil {
		info.ChatToGeminiStreamState = state
	}
	return state, nil
}

func sendGeminiStreamResults(c *gin.Context, results []relayconvert.ResponseResult) *types.NewAPIError {
	for _, result := range results {
		geminiResponse, ok := result.Value.(*dto.GeminiChatResponse)
		if !ok {
			return types.NewError(fmt.Errorf("expected Gemini stream response, got %T", result.Value), types.ErrorCodeBadResponseBody)
		}
		if geminiResponse == nil {
			continue
		}
		data, err := common.Marshal(geminiResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		c.Render(-1, common.CustomEvent{Data: "data: " + string(data)})
		_ = helper.FlushWriter(c)
	}
	return nil
}

func countClaudeStreamBillableTools(info *relaycommon.RelayInfo, claudeResponse *dto.ClaudeResponse) {
	if claudeResponse == nil {
		return
	}
	if claudeResponse.Type == "content_block_start" &&
		claudeResponse.ContentBlock != nil &&
		claudeResponse.ContentBlock.Type == "tool_use" {
		info.CountBillableToolCall(dto.BuildInCallToolUse, claudeResponse.ContentBlock.Name)
	}
	if claudeResponse.Type == "message_delta" && claudeResponse.Usage != nil {
		// message_delta usage is cumulative, so the last report wins.
		recordClaudeServerToolUse(info, claudeResponse.Usage.ServerToolUse)
	}
}

// recordClaudeServerToolUse bills the server tool requests Anthropic reports
// in usage.server_tool_use. web_fetch, code_execution, and tool_search have no
// built-in price and bill only once an operator prices them.
func recordClaudeServerToolUse(info *relaycommon.RelayInfo, serverToolUse *dto.ClaudeServerToolUse) {
	if info == nil || serverToolUse == nil {
		return
	}
	for name, count := range map[string]int{
		cmp.Or(info.WebSearchBillingKey, dto.BuildInToolWebSearch): serverToolUse.WebSearchRequests,
		"web_fetch":      serverToolUse.WebFetchRequests,
		"code_execution": serverToolUse.CodeExecutionRequests,
		"tool_search":    serverToolUse.ToolSearchRequests,
	} {
		if count > 0 {
			info.SetBillableToolCount(name, count)
		}
	}
}

func HandleStreamFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo) {
	if claudeInfo.Usage.PromptTokens == 0 {
		//上游出错
	}
	if claudeInfo.Usage.CompletionTokens == 0 || !claudeInfo.Done {
		if common.DebugEnabled {
			common.SysLog(common.LogText("claude response usage is not complete, maybe upstream error"))
		}
		// 只补缺失字段，不整份覆盖——保留 message_start 已拿到的 cache 字段
		fallback := service.ResponseText2Usage(c, claudeInfo.ResponseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		// Each tool_use block costs about 40 output tokens beyond its name and JSON
		// input (measured on Claude Haiku 4.5 and Sonnet 5 agent turns).
		fallback.CompletionTokens += claudeInfo.ToolUseCount * 40
		if claudeInfo.Usage.CompletionTokens == 0 ||
			(!claudeInfo.Done && fallback.CompletionTokens > claudeInfo.Usage.CompletionTokens) {
			claudeInfo.Usage.CompletionTokens = fallback.CompletionTokens
		}
		if claudeInfo.Usage.PromptTokens == 0 {
			claudeInfo.Usage.PromptTokens = fallback.PromptTokens
		}
		claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
	}
	if claudeInfo.Usage != nil {
		claudeInfo.Usage.UsageSemantic = "anthropic"
	}
	relayconvert.FinalizeClaudeStreamBillingUsage(claudeInfo)

	if info.RelayFormat == types.RelayFormatClaude {
		//
	} else if info.RelayFormat == types.RelayFormatOpenAI {
		if info.ShouldIncludeUsage {
			openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
			response := helper.GenerateFinalUsageResponse(claudeInfo.ResponseId, claudeInfo.Created, info.UpstreamModelName, openAIUsage)
			err := helper.ObjectData(c, response)
			if err != nil {
				common.SysLog(common.LogText("send final response failed: %s", err.Error()))
			}
		}
		helper.Done(c)
	} else if info.RelayFormat == types.RelayFormatGemini {
		state, err := claudeToGeminiStreamState(info)
		if err != nil {
			common.SysLog(common.LogText("error creating Gemini stream state: %s", err.Error()))
			return
		}
		results, err := service.FinalizeStreamResponse(c, info, state)
		if err != nil {
			common.SysLog(common.LogText("error finalizing Gemini stream response: %s", err.Error()))
			return
		}
		if sendErr := sendGeminiStreamResults(c, results); sendErr != nil {
			common.SysLog(common.LogText("send final Gemini stream response failed: %s", sendErr.Error()))
		}
	}
}

func ClaudeStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var conversion *relayconvert.ResponseStreamState
	if info.RelayFormat == types.RelayFormatOpenAIResponses || info.RelayFormat == types.RelayFormatGemini {
		var convertErr error
		conversion, convertErr = relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, info.RelayFormat, relayconvert.ResponseStreamOptions{
			ID: claudeInfo.ResponseId, Model: info.UpstreamModelName, Created: claudeInfo.Created,
		})
		if convertErr != nil {
			return nil, types.NewError(convertErr, types.ErrorCodeBadResponse)
		}
	}
	var err *types.NewAPIError
	helper.StreamScannerHandlerWithOptions(c, resp, info, helper.StreamScannerOptions{
		RequireExplicitTerminal: true,
		RequiredTerminalEvent:   "message_stop",
	}, func(data string, sr *helper.StreamResult) {
		err = HandleStreamResponseData(c, info, claudeInfo, data, conversion)
		if err != nil {
			sr.Stop(err)
			return
		}
		var event struct {
			Type string `json:"type"`
		}
		if common.UnmarshalJsonStr(data, &event) == nil && event.Type == "message_stop" {
			sr.DoneWithTerminal("message_stop", "completed")
		}
	})
	info.StreamStatus.RequireTerminal()
	if err != nil {
		fallback := service.ResponseText2Usage(c, claudeInfo.ResponseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		if claudeInfo.Usage.CompletionTokens == 0 {
			claudeInfo.Usage.CompletionTokens = fallback.CompletionTokens
		}
		if claudeInfo.Usage.PromptTokens == 0 {
			claudeInfo.Usage.PromptTokens = fallback.PromptTokens
		}
		claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
		claudeInfo.Usage.UsageSemantic = "anthropic"
		if info.StreamStatus != nil {
			info.StreamStatus.ObservePartialUsage(claudeInfo.Usage, false, true)
		}
		return claudeInfo.Usage, err
	}
	if statusErr := helper.StreamStatusError(c, info); statusErr != nil {
		fallback := service.ResponseText2Usage(c, claudeInfo.ResponseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		if claudeInfo.Usage.CompletionTokens == 0 {
			claudeInfo.Usage.CompletionTokens = fallback.CompletionTokens
		}
		if claudeInfo.Usage.PromptTokens == 0 {
			claudeInfo.Usage.PromptTokens = fallback.PromptTokens
		}
		claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
		claudeInfo.Usage.UsageSemantic = "anthropic"
		if info.StreamStatus != nil {
			info.StreamStatus.ObservePartialUsage(claudeInfo.Usage, false, true)
		}
		return claudeInfo.Usage, statusErr
	}

	HandleStreamFinalResponse(c, info, claudeInfo)
	if conversion != nil {
		// Only finalize after the scanner has verified the upstream terminal event.
		usage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
		chunk := helper.GenerateFinalUsageResponse(claudeInfo.ResponseId, claudeInfo.Created, info.UpstreamModelName, usage)
		results, convertErr := relayconvert.ConvertStreamResponseChunk(c, info, conversion, chunk)
		if convertErr != nil {
			return claudeInfo.Usage, types.NewError(convertErr, types.ErrorCodeBadResponse)
		}
		if sendErr := writeClaudeConvertedEvents(c, info, results); sendErr != nil {
			return claudeInfo.Usage, sendErr
		}
		results, convertErr = relayconvert.FinalizeStreamResponse(c, info, conversion)
		if convertErr != nil {
			return claudeInfo.Usage, types.NewError(convertErr, types.ErrorCodeBadResponse)
		}
		if sendErr := writeClaudeConvertedEvents(c, info, results); sendErr != nil {
			return claudeInfo.Usage, sendErr
		}
	}
	return claudeInfo.Usage, nil
}

func HandleClaudeResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, httpResp *http.Response, data []byte) *types.NewAPIError {
	var claudeResponse dto.ClaudeResponse
	err := common.Unmarshal(data, &claudeResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	info.ObserveResponseModel(claudeResponse.Model)
	info.MergeResponseSemantics(types.RelayFormatClaude, data)
	maybeMarkClaudeRefusal(c, info, claudeResponse.StopReason)
	if claudeInfo.Usage == nil {
		claudeInfo.Usage = &dto.Usage{}
	}
	if claudeResponse.Usage != nil {
		claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens
		claudeInfo.Usage.CompletionTokens = claudeResponse.Usage.OutputTokens
		claudeInfo.Usage.TotalTokens = claudeResponse.Usage.InputTokens + claudeResponse.Usage.OutputTokens
		claudeInfo.Usage.UsageSemantic = "anthropic"
		claudeInfo.Usage.BillingUsage = dto.CloneBillingUsage(claudeResponse.Usage.BillingUsage)
		if claudeInfo.Usage.BillingUsage == nil {
			claudeInfo.Usage.BillingUsage = dto.NewClaudeMessagesBillingUsage(claudeResponse.Usage)
		}
		claudeInfo.Usage.PromptTokensDetails.CachedTokens = claudeResponse.Usage.CacheReadInputTokens
		claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens = claudeResponse.Usage.CacheCreationInputTokens
		claudeInfo.Usage.ClaudeCacheCreation5mTokens = claudeResponse.Usage.GetCacheCreation5mTokens()
		claudeInfo.Usage.ClaudeCacheCreation1hTokens = claudeResponse.Usage.GetCacheCreation1hTokens()
	}
	var responseData []byte
	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		openaiResponse := ResponseClaude2OpenAI(&claudeResponse)
		openaiResponse.Usage = buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
		responseData, err = common.Marshal(openaiResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatOpenAIResponses:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatOpenAIResponses, &claudeResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responsesResponse, ok := convertResult.Value.(*dto.OpenAIResponsesResponse)
		if !ok {
			return types.NewError(fmt.Errorf("expected OpenAI Responses response, got %T", convertResult.Value), types.ErrorCodeBadResponseBody)
		}
		if responseID := helper.GetResponseID(c); responseID != "" {
			responsesResponse.ID = responseID
		}
		responseData, err = common.Marshal(responsesResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatClaude:
		responseData = data
	case types.RelayFormatGemini:
		converted, convertErr := relayconvert.ConvertResponse(c, info, info.RelayFormat, &claudeResponse)
		if convertErr != nil {
			return types.NewError(convertErr, types.ErrorCodeBadResponseBody)
		}
		responseData, err = common.Marshal(converted.Value)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	}

	if claudeResponse.Usage != nil {
		recordClaudeServerToolUse(info, claudeResponse.Usage.ServerToolUse)
	}

	for _, block := range claudeResponse.Content {
		if block.Type == "tool_use" {
			info.CountBillableToolCall(dto.BuildInCallToolUse, block.Name)
		}
	}

	service.IOCopyBytesGracefully(c, httpResp, responseData)
	return nil
}

func ClaudeHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	logger.LogDebug(c, "responseBody: %s", responseBody)
	handleErr := HandleClaudeResponseData(c, info, claudeInfo, resp, responseBody)
	if handleErr != nil {
		return nil, handleErr
	}
	return claudeInfo.Usage, nil
}

// writeClaudeConvertedEvents writes registered conversion results using the client wire format.
func writeClaudeConvertedEvents(c *gin.Context, info *relaycommon.RelayInfo, results []relayconvert.ResponseResult) *types.NewAPIError {
	for _, result := range results {
		var data []byte
		var err error
		if event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent); ok {
			data, err = common.Marshal(event.Payload)
			if err == nil {
				helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, string(data))
			}
		} else {
			data, err = common.Marshal(result.Value)
			if err == nil {
				err = helper.ObjectData(c, result.Value)
			}
		}
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponse)
		}
		if info.StreamStatus != nil {
			observation := helper.ObserveStreamDataPayload(string(data), info.RelayFormat)
			if observation.Meaningful {
				info.StreamStatus.MarkClientPayloadCommitted()
			}
			info.StreamStatus.ObserveToolPayloadBytes(observation.ToolNameBytes, observation.ToolArgumentBytes)
		}
	}
	return nil
}
