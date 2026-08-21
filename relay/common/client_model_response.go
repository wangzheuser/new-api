package common

import (
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var clientModelContainers = []string{"", "response", "message", "session"}

var clientModelKeys = []string{
	"model",
	"model_id",
	"modelId",
	"model_name",
	"modelName",
	"model_version",
	"modelVersion",
}

// RewriteClientModelJSON rewrites only protocol-level model and structured error fields.
func RewriteClientModelJSON(data []byte, info *RelayInfo, statusCode int) ([]byte, bool) {
	requestedModel := strings.TrimSpace(info.GetRequestedModelName())
	if len(data) == 0 || requestedModel == "" || !gjson.ValidBytes(data) {
		return data, false
	}

	result := data
	changed := false
	for _, container := range clientModelContainers {
		for _, key := range clientModelKeys {
			path := key
			if container != "" {
				path = container + "." + key
			}
			field := gjson.GetBytes(result, path)
			if !field.Exists() || field.Type == gjson.String && field.String() == requestedModel {
				continue
			}
			updated, err := sjson.SetBytes(result, path, requestedModel)
			if err != nil {
				continue
			}
			result = updated
			changed = true
		}
	}

	isErrorPayload := statusCode >= http.StatusBadRequest || clientModelErrorPayload(result)
	for _, path := range []string{"error.message", "response.error.message"} {
		result, changed = rewriteClientModelErrorPath(result, path, info, changed)
	}
	if isErrorPayload {
		result, changed = rewriteClientModelErrorPath(result, "message", info, changed)
	}
	return result, changed
}

// ClientInternalModelNames returns the model identifiers that must stay internal.
func ClientInternalModelNames(info *RelayInfo) []string {
	if info == nil {
		return nil
	}
	requestedModel := strings.TrimSpace(info.GetRequestedModelName())
	names := []string{
		info.AttemptModelName,
		info.GetAttemptModelName(),
		info.OriginModelName,
	}
	if info.ChannelMeta != nil {
		names = append(names, info.ChannelMeta.UpstreamModelName)
	}
	if info.ContextFallback != nil && info.ContextFallback.Applied {
		names = append(names, info.ContextFallback.SourceModel, info.ContextFallback.FallbackModel)
	}

	seen := make(map[string]struct{}, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name == requestedModel {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return len(result[i]) > len(result[j])
	})
	return result
}

// SanitizeClientModelErrorMessage replaces known internal model identifiers in an error message.
func SanitizeClientModelErrorMessage(message string, info *RelayInfo) string {
	requestedModel := strings.TrimSpace(info.GetRequestedModelName())
	if message == "" || requestedModel == "" {
		return message
	}
	result := message
	for _, internalModel := range ClientInternalModelNames(info) {
		result = replaceModelIdentifier(result, internalModel, requestedModel)
	}
	return result
}

// FilterClientModelResponseHeaders removes upstream headers that can disclose internal model identifiers.
func FilterClientModelResponseHeaders(header http.Header, info *RelayInfo) {
	if header == nil {
		return
	}
	internalModels := ClientInternalModelNames(info)
	for key, values := range header {
		if strings.Contains(strings.ToLower(key), "model") {
			header.Del(key)
			continue
		}
		for _, value := range values {
			for _, internalModel := range internalModels {
				if strings.Contains(value, internalModel) {
					header.Del(key)
					break
				}
			}
			if _, exists := header[key]; !exists {
				break
			}
		}
	}
}

// ClearTransformedEntityHeaders removes validators and ranges invalidated by a transformed body.
func ClearTransformedEntityHeaders(header http.Header) {
	if header == nil {
		return
	}
	for _, key := range []string{
		"Content-Length",
		"Content-Encoding",
		"Content-Range",
		"ETag",
		"Last-Modified",
		"Content-MD5",
		"Digest",
	} {
		header.Del(key)
	}
}

// clientModelErrorPayload reports whether a successful transport payload is an error event.
func clientModelErrorPayload(data []byte) bool {
	if gjson.GetBytes(data, "error").Exists() || gjson.GetBytes(data, "response.error").Exists() {
		return true
	}
	eventType := strings.ToLower(gjson.GetBytes(data, "type").String())
	return eventType == "error" || strings.HasSuffix(eventType, ".error") ||
		eventType == "response.failed" || eventType == "response.cancelled"
}

// rewriteClientModelErrorPath sanitizes one recognized structured error message.
func rewriteClientModelErrorPath(data []byte, path string, info *RelayInfo, changed bool) ([]byte, bool) {
	field := gjson.GetBytes(data, path)
	if !field.Exists() || field.Type != gjson.String {
		return data, changed
	}
	sanitized := SanitizeClientModelErrorMessage(field.String(), info)
	if sanitized == field.String() {
		return data, changed
	}
	updated, err := sjson.SetBytes(data, path, sanitized)
	if err != nil {
		return data, changed
	}
	return updated, true
}

// replaceModelIdentifier replaces exact identifiers without matching a longer model token.
func replaceModelIdentifier(message string, internalModel string, requestedModel string) string {
	if message == "" || internalModel == "" || internalModel == requestedModel {
		return message
	}

	searchFrom := 0
	lastWrite := 0
	changed := false
	var result strings.Builder
	for searchFrom <= len(message)-len(internalModel) {
		relativeIndex := strings.Index(message[searchFrom:], internalModel)
		if relativeIndex < 0 {
			break
		}
		start := searchFrom + relativeIndex
		end := start + len(internalModel)
		if modelIdentifierBoundary(message, start, end) {
			if !changed {
				result.Grow(len(message))
			}
			result.WriteString(message[lastWrite:start])
			result.WriteString(requestedModel)
			lastWrite = end
			changed = true
		}
		searchFrom = end
	}
	if !changed {
		return message
	}
	result.WriteString(message[lastWrite:])
	return result.String()
}

// modelIdentifierBoundary checks the runes immediately surrounding a match.
func modelIdentifierBoundary(value string, start int, end int) bool {
	if start > 0 {
		before, _ := utf8.DecodeLastRuneInString(value[:start])
		if isModelIdentifierRune(before) {
			return false
		}
	}
	if end < len(value) {
		after, _ := utf8.DecodeRuneInString(value[end:])
		if isModelIdentifierRune(after) {
			return false
		}
	}
	return true
}

// isModelIdentifierRune defines the characters that can continue a model identifier.
func isModelIdentifierRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || strings.ContainsRune("._-/:@+", value)
}
