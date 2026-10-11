package controller

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

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
		common.SysError(fmt.Sprintf("failed to commit buffered relay response: %s", err.Error()))
	}
	return nil
}

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

func recordRelayErrorLog(c *gin.Context, relayInfo *relaycommon.RelayInfo, err *types.NewAPIError, content string, rawError *types.NewAPIError, isIntermediate bool) {
	if err == nil || !constant.ErrorLogEnabled || !types.IsRecordErrorLog(err) {
		return
	}
	if content == "" {
		content = err.MaskSensitiveErrorWithStatusCode()
	}
	channelID := c.GetInt("channel_id")
	if relayInfo != nil && relayInfo.IsContextFallbackActive() {
		channelID = relayInfo.ContextFallback.SourceChannelID
	}
	other := map[string]any{
		"error_type":   err.GetErrorType(),
		"error_code":   err.GetErrorCode(),
		"status_code":  err.StatusCode,
		"intermediate": isIntermediate,
		"admin_info":   map[string]any{"use_channel": c.GetStringSlice("use_channel")},
	}
	adminInfo := other["admin_info"].(map[string]any)
	service.AppendResponseOverrideAdminInfo(relayInfo, adminInfo)
	service.AppendRelayProtocolInfo(relayInfo, nil, adminInfo)
	if relayInfo != nil {
		if len(relayInfo.RequestConversionChain) > 0 {
			conversion := make([]string, 0, len(relayInfo.RequestConversionChain))
			for _, format := range relayInfo.RequestConversionChain {
				switch format {
				case types.RelayFormatOpenAI:
					conversion = append(conversion, "OpenAI Compatible")
				case types.RelayFormatClaude:
					conversion = append(conversion, "Claude Messages")
				case types.RelayFormatGemini:
					conversion = append(conversion, "Google Gemini")
				case types.RelayFormatOpenAIResponses:
					conversion = append(conversion, "OpenAI Responses")
				default:
					conversion = append(conversion, string(format))
				}
			}
			other["request_conversion"] = conversion
		}
		if finalFormat := relayInfo.GetFinalRequestRelayFormat(); finalFormat != "" {
			other["final_request_relay_format"] = finalFormat
		}
	}
	if relayInfo != nil && relayInfo.GetUpstreamModelName() != "" {
		other["upstream_model_name"] = relayInfo.GetUpstreamModelName()
	}
	if rawError != nil {
		other["public_error"] = true
		if status, ok := rawError.GetUpstreamStatusCode(); ok {
			other["admin_info"].(map[string]any)["upstream_status_code"] = status
		}
	}
	start := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
	if start.IsZero() {
		start = time.Now()
	}
	model.RecordErrorLog(c, c.GetInt("id"), channelID, c.GetString("original_model"), c.GetString("token_name"), content, c.GetInt("token_id"), int(time.Since(start).Seconds()), common.GetContextKeyBool(c, constant.ContextKeyIsStream), isIntermediate, c.GetString("group"), other)
}
