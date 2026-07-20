package service

import (
	"bytes"
	"context"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const conversationBinaryOmitted = "[binary data omitted]"

var conversationDataURIRegexp = regexp.MustCompile(`(?i)data:[^,"\\\s]*;base64,[A-Za-z0-9+/=_-]*`)

// RecordConversationLog persists the bounded final relay attempt without affecting the relay result.
func RecordConversationLog(c *gin.Context, info *relaycommon.RelayInfo, relayErr *types.NewAPIError) {
	if c == nil || info == nil || info.ConversationCapture == nil {
		return
	}
	snapshot := info.ConversationCapture.Snapshot()
	if len(snapshot.ClientRequestBody) == 0 && len(snapshot.ClientResponseBody) == 0 {
		return
	}
	clientRequestBody, clientRequestOmitted := sanitizeConversationBody(snapshot.ClientRequestBody)
	upstreamRequestBody, upstreamRequestOmitted := sanitizeConversationBody(snapshot.UpstreamRequestBody)
	upstreamResponseBody, upstreamResponseOmitted := sanitizeConversationBody(snapshot.UpstreamResponseBody)
	clientResponseBody, clientResponseOmitted := sanitizeConversationBody(snapshot.ClientResponseBody)
	metadata := map[string]interface{}{
		"retry_index":                info.RetryIndex,
		"attempted_channel_ids":      c.GetStringSlice("use_channel"),
		"binary_payloads_omitted":    clientRequestOmitted || upstreamRequestOmitted || upstreamResponseOmitted || clientResponseOmitted,
		"client_request_bytes":       snapshot.ClientRequestBytes,
		"upstream_request_bytes":     snapshot.UpstreamRequestBytes,
		"upstream_response_bytes":    snapshot.UpstreamResponseBytes,
		"client_response_bytes":      snapshot.ClientResponseBytes,
		"request_conversion_chain":   info.RequestConversionChain,
		"final_request_relay_format": info.GetFinalRequestRelayFormat(),
	}
	if relayErr != nil {
		metadata["error_code"] = relayErr.GetErrorCode()
	}
	metadataBytes, err := common.Marshal(metadata)
	if err != nil {
		logger.LogError(c, "failed to marshal conversation log metadata: "+err.Error())
		return
	}
	statusCode := c.Writer.Status()
	if relayErr != nil && relayErr.StatusCode > 0 {
		statusCode = relayErr.StatusCode
	}
	if statusCode == 0 {
		statusCode = 200
	}

	log := &model.ConversationLog{
		CreatedAt:                 common.GetTimestamp(),
		RequestId:                 c.GetString(common.RequestIdKey),
		UserId:                    info.UserId,
		Username:                  c.GetString("username"),
		TokenId:                   info.TokenId,
		ChannelId:                 info.ChannelId,
		Group:                     info.UsingGroup,
		ModelName:                 info.OriginModelName,
		UpstreamModelName:         info.UpstreamModelName,
		RelayFormat:               string(info.RelayFormat),
		RequestPath:               info.RequestURLPath,
		IsStream:                  info.IsStream,
		StatusCode:                statusCode,
		ClientRequestBody:         string(clientRequestBody),
		UpstreamRequestBody:       string(upstreamRequestBody),
		UpstreamResponseBody:      string(upstreamResponseBody),
		ClientResponseBody:        string(clientResponseBody),
		Metadata:                  string(metadataBytes),
		ClientRequestTruncated:    snapshot.ClientRequestTruncated,
		UpstreamRequestTruncated:  snapshot.UpstreamRequestTruncated,
		UpstreamResponseTruncated: snapshot.UpstreamResponseTruncated,
		ClientResponseTruncated:   snapshot.ClientResponseTruncated,
	}
	log.StorageBytes = int64(len(log.ClientRequestBody) + len(log.UpstreamRequestBody) +
		len(log.UpstreamResponseBody) + len(log.ClientResponseBody) + len(log.Metadata))
	if err := model.CreateConversationLog(log); err != nil {
		logger.LogError(c, "failed to record conversation log: "+err.Error())
	}
}

// sanitizeConversationBody removes encoded binary payloads while preserving ordinary JSON and SSE bodies verbatim.
func sanitizeConversationBody(body []byte) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	if sanitized, changed := sanitizeConversationJSON(body); changed {
		return redactConversationDataURIs(sanitized, true)
	}

	lines := bytes.Split(body, []byte{'\n'})
	changed := false
	for index, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(trimmed[len("data:"):])
		sanitized, lineChanged := sanitizeConversationJSON(payload)
		if !lineChanged {
			continue
		}
		payloadStart := bytes.Index(line, payload)
		lines[index] = append(append([]byte(nil), line[:payloadStart]...), sanitized...)
		changed = true
	}
	return redactConversationDataURIs(bytes.Join(lines, []byte{'\n'}), changed)
}

// sanitizeConversationJSON redacts known binary fields from one JSON document.
func sanitizeConversationJSON(body []byte) ([]byte, bool) {
	var value interface{}
	if err := common.Unmarshal(body, &value); err != nil || !redactConversationBinary(value, false) {
		return body, false
	}
	sanitized, err := common.Marshal(value)
	if err != nil {
		return body, false
	}
	return sanitized, true
}

// redactConversationBinary replaces base64 media fields in a decoded JSON value.
func redactConversationBinary(value interface{}, binaryContext bool) bool {
	changed := false
	switch current := value.(type) {
	case map[string]interface{}:
		base64Object := binaryContext
		if kind, ok := current["type"].(string); ok && strings.EqualFold(kind, "base64") {
			base64Object = true
		}
		for key, child := range current {
			childContext := base64Object || isConversationBinaryContainer(key)
			if text, ok := child.(string); ok {
				if key == "b64_json" || key == "file_data" || (key == "data" && childContext) || isConversationDataURI(text) {
					current[key] = conversationBinaryOmitted
					changed = true
				}
				continue
			}
			if redactConversationBinary(child, childContext) {
				changed = true
			}
		}
	case []interface{}:
		for _, child := range current {
			if redactConversationBinary(child, binaryContext) {
				changed = true
			}
		}
	}
	return changed
}

// isConversationBinaryContainer identifies common multimodal payload containers.
func isConversationBinaryContainer(key string) bool {
	switch key {
	case "inlineData", "inline_data", "input_audio", "audio":
		return true
	default:
		return false
	}
}

// isConversationDataURI reports whether a string contains an encoded data URI.
func isConversationDataURI(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	comma := strings.IndexByte(normalized, ',')
	return strings.HasPrefix(normalized, "data:") && comma > 0 && strings.Contains(normalized[:comma], ";base64")
}

// redactConversationDataURIs covers plain text and truncated JSON data URIs.
func redactConversationDataURIs(body []byte, changed bool) ([]byte, bool) {
	redacted := conversationDataURIRegexp.ReplaceAll(body, []byte(conversationBinaryOmitted))
	return redacted, changed || !bytes.Equal(redacted, body)
}

// CleanupConversationLogs applies retention and total-storage limits.
func CleanupConversationLogs(ctx context.Context) (map[string]int64, error) {
	result := map[string]int64{"expired": 0, "trimmed": 0}
	if common.ConversationLogRetentionDays > 0 {
		cutoff := common.GetTimestamp() - int64(common.ConversationLogRetentionDays)*24*60*60
		deleted, err := model.DeleteConversationLogs(ctx, model.ConversationLogQuery{EndTime: cutoff}, 200)
		if err != nil {
			return result, err
		}
		result["expired"] = deleted
	}
	if common.ConversationLogMaxStorageGB > 0 {
		deleted, err := model.TrimConversationLogs(ctx, int64(common.ConversationLogMaxStorageGB)*1024*1024*1024, 200)
		if err != nil {
			return result, err
		}
		result["trimmed"] = deleted
	}
	return result, nil
}
