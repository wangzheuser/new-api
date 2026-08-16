package common

import (
	"strings"

	"github.com/QuantumNous/new-api/types"
	"github.com/tidwall/gjson"
)

const (
	ResponseTransportSuccess = "success"
	ResponseTransportError   = "error"
	ResponseTransportUnknown = "unknown"

	ResponseOutcomeCompleted  = "completed"
	ResponseOutcomeToolCall   = "tool_call"
	ResponseOutcomeIncomplete = "incomplete"
	ResponseOutcomeRejected   = "rejected"
	ResponseOutcomeFailed     = "failed"
	ResponseOutcomeUnknown    = "unknown"

	ResponseRejectionNone    = "none"
	ResponseRejectionPartial = "partial"
	ResponseRejectionAll     = "all"

	ResponseOutputText             = "text"
	ResponseOutputToolCalls        = "tool_calls"
	ResponseOutputTextAndToolCalls = "text_and_tool_calls"
	ResponseOutputReasoningOnly    = "reasoning_only"
	ResponseOutputEmpty            = "empty"
	ResponseOutputAbsent           = "absent"

	ResponseUsageUpstream  = "upstream"
	ResponseUsageEstimated = "estimated"
	ResponseUsageAbsent    = "absent"

	ResponseStreamNotStreamed = "not_streamed"
	ResponseStreamComplete    = "complete"
	ResponseStreamIncomplete  = "incomplete"
	ResponseStreamError       = "error"
)

// ResponseSemanticItem preserves independent facts for one provider choice, candidate, or output item.
type ResponseSemanticItem struct {
	Index            int    `json:"index"`
	PrimaryOutcome   string `json:"primary_outcome"`
	RejectionState   string `json:"rejection_state"`
	OutputState      string `json:"output_state"`
	ProviderReason   string `json:"provider_reason,omitempty"`
	NormalizedReason string `json:"normalized_reason,omitempty"`
	HasText          bool   `json:"has_text"`
	HasReasoning     bool   `json:"has_reasoning"`
	HasToolCalls     bool   `json:"has_tool_calls"`
	Incomplete       bool   `json:"incomplete"`
	Failed           bool   `json:"failed"`
	Truncated        bool   `json:"truncated"`
}

// ResponseSemanticSummary is the protocol-independent aggregate exposed to response rules.
type ResponseSemanticSummary struct {
	TransportStatus  string                 `json:"transport_status"`
	PrimaryOutcome   string                 `json:"primary_outcome"`
	RejectionState   string                 `json:"rejection_state"`
	OutputState      string                 `json:"output_state"`
	ProviderReason   string                 `json:"provider_reason,omitempty"`
	NormalizedReason string                 `json:"normalized_reason,omitempty"`
	HasText          bool                   `json:"has_text"`
	HasReasoning     bool                   `json:"has_reasoning"`
	HasToolCalls     bool                   `json:"has_tool_calls"`
	Truncated        bool                   `json:"truncated"`
	UsageState       string                 `json:"usage_state"`
	StreamState      string                 `json:"stream_state"`
	Items            []ResponseSemanticItem `json:"items"`
}

// ResponseEndpointSemantics identifies one side of the relay without conflating status codes.
type ResponseEndpointSemantics struct {
	Format      types.RelayFormat `json:"format,omitempty"`
	HTTPStatus  int               `json:"http_status,omitempty"`
	Model       string            `json:"model,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
}

// ResponseSemantics is the stable semantic condition root: response, upstream, and client.
type ResponseSemantics struct {
	Response ResponseSemanticSummary   `json:"response"`
	Upstream ResponseEndpointSemantics `json:"upstream"`
	Client   ResponseEndpointSemantics `json:"client"`
}

// ClassifyResponseSemantics classifies one complete provider JSON response.
// Invalid or unsupported payloads produce unknown facts so runtime callers can fail open.
func ClassifyResponseSemantics(format types.RelayFormat, body []byte) ResponseSemantics {
	semantics := newUnknownResponseSemantics()
	semantics.Upstream.Format = format
	if !gjson.ValidBytes(body) {
		return semantics
	}
	root := gjson.ParseBytes(body)
	if meaningfulJSON(root.Get("error")) {
		semantics.Response.Items = []ResponseSemanticItem{{
			PrimaryOutcome:   ResponseOutcomeFailed,
			RejectionState:   ResponseRejectionNone,
			OutputState:      ResponseOutputEmpty,
			NormalizedReason: "embedded_error",
			Failed:           true,
		}}
		return aggregateResponseSemantics(semantics)
	}
	switch format {
	case types.RelayFormatClaude:
		return classifyClaudeResponse(root, semantics)
	case types.RelayFormatGemini:
		return classifyGeminiResponse(root, semantics)
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return classifyResponsesResponse(root, semantics)
	default:
		return classifyOpenAIResponse(root, semantics)
	}
}

// MergeResponseSemantics accumulates provider-native and client-visible facts across conversion.
func MergeResponseSemantics(base ResponseSemantics, additional ResponseSemantics) ResponseSemantics {
	if isZeroResponseSemantics(base) {
		return additional
	}
	if isZeroResponseSemantics(additional) {
		return base
	}
	result := base
	if result.Upstream.Format == "" {
		result.Upstream = additional.Upstream
	}
	if additional.Upstream.HTTPStatus != 0 && result.Upstream.HTTPStatus == 0 {
		result.Upstream.HTTPStatus = additional.Upstream.HTTPStatus
	}
	if additional.Client.Format != "" {
		result.Client.Format = additional.Client.Format
	}
	if additional.Client.HTTPStatus != 0 {
		result.Client.HTTPStatus = additional.Client.HTTPStatus
	}
	result.Response.Items = mergeResponseSemanticItems(result.Response.Items, additional.Response.Items)
	result.Response.HasText = result.Response.HasText || additional.Response.HasText
	result.Response.HasReasoning = result.Response.HasReasoning || additional.Response.HasReasoning
	result.Response.HasToolCalls = result.Response.HasToolCalls || additional.Response.HasToolCalls
	result.Response.Truncated = result.Response.Truncated || additional.Response.Truncated
	if result.Response.ProviderReason == "" {
		result.Response.ProviderReason = additional.Response.ProviderReason
	}
	if result.Response.NormalizedReason == "" {
		result.Response.NormalizedReason = additional.Response.NormalizedReason
	}
	result.Response.UsageState = mergeUsageState(result.Response.UsageState, additional.Response.UsageState)
	result.Response.StreamState = mergeStreamState(result.Response.StreamState, additional.Response.StreamState)
	return aggregateResponseSemantics(result)
}

// MergeResponseSemantics classifies provider-native bytes and accumulates them on this relay attempt.
func (info *RelayInfo) MergeResponseSemantics(format types.RelayFormat, body []byte) ResponseSemantics {
	classified := ClassifyResponseSemantics(format, body)
	if info == nil {
		return classified
	}
	info.ResponseSemantics = MergeResponseSemantics(info.ResponseSemantics, classified)
	return info.ResponseSemantics
}

// SetResponseUsageState records the effective usage provenance after provider
// parsing and any local estimation have completed for the current attempt.
func (info *RelayInfo) SetResponseUsageState(state string) {
	if info == nil {
		return
	}
	switch state {
	case ResponseUsageUpstream, ResponseUsageEstimated, ResponseUsageAbsent:
		info.ResponseSemantics.Response.UsageState = state
	}
}

func newUnknownResponseSemantics() ResponseSemantics {
	return ResponseSemantics{Response: ResponseSemanticSummary{
		TransportStatus: ResponseTransportUnknown,
		PrimaryOutcome:  ResponseOutcomeUnknown,
		RejectionState:  ResponseRejectionNone,
		OutputState:     ResponseOutputAbsent,
		UsageState:      ResponseUsageAbsent,
		StreamState:     ResponseStreamNotStreamed,
		Items:           []ResponseSemanticItem{},
	}}
}

func classifyOpenAIResponse(root gjson.Result, semantics ResponseSemantics) ResponseSemantics {
	if root.Get("usage").IsObject() {
		semantics.Response.UsageState = ResponseUsageUpstream
	}
	for index, choice := range root.Get("choices").Array() {
		message := choice.Get("message")
		semantics.Response.Items = append(semantics.Response.Items, classifySemanticItem(
			index,
			choice.Get("finish_reason").String(),
			hasTextValue(message.Get("content")) || hasTextValue(choice.Get("text")),
			nonEmptyArray(message.Get("tool_calls")) || meaningfulJSON(message.Get("function_call")),
			hasTextValue(message.Get("reasoning_content")) || hasTextValue(message.Get("reasoning")),
			meaningfulJSON(message.Get("refusal")),
		))
	}
	return aggregateResponseSemantics(semantics)
}

func classifyClaudeResponse(root gjson.Result, semantics ResponseSemantics) ResponseSemantics {
	if root.Get("usage").IsObject() {
		semantics.Response.UsageState = ResponseUsageUpstream
	}
	hasText, hasTool, hasReasoning, hasRefusal := false, false, false, false
	for _, block := range root.Get("content").Array() {
		switch strings.ToLower(block.Get("type").String()) {
		case "text", "output_text":
			hasText = hasText || hasTextValue(block.Get("text"))
		case "tool_use", "server_tool_use", "function_call":
			hasTool = true
		case "thinking", "redacted_thinking", "reasoning":
			hasReasoning = true
		case "refusal":
			hasRefusal = true
		}
	}
	semantics.Response.Items = append(semantics.Response.Items, classifySemanticItem(0, root.Get("stop_reason").String(), hasText, hasTool, hasReasoning, hasRefusal))
	return aggregateResponseSemantics(semantics)
}

func classifyGeminiResponse(root gjson.Result, semantics ResponseSemantics) ResponseSemantics {
	if root.Get("usageMetadata").IsObject() {
		semantics.Response.UsageState = ResponseUsageUpstream
	}
	blockReason := strings.TrimSpace(root.Get("promptFeedback.blockReason").String())
	if blockReason != "" && len(root.Get("candidates").Array()) == 0 {
		semantics.Response.Items = []ResponseSemanticItem{classifySemanticItem(0, blockReason, false, false, false, true)}
		return aggregateResponseSemantics(semantics)
	}
	for index, candidate := range root.Get("candidates").Array() {
		hasText, hasTool, hasReasoning := false, false, false
		for _, part := range candidate.Get("content.parts").Array() {
			if hasTextValue(part.Get("text")) {
				if part.Get("thought").Bool() {
					hasReasoning = true
				} else {
					hasText = true
				}
			}
			hasTool = hasTool || meaningfulJSON(part.Get("functionCall")) || meaningfulJSON(part.Get("function_call"))
		}
		semantics.Response.Items = append(semantics.Response.Items, classifySemanticItem(index, candidate.Get("finishReason").String(), hasText, hasTool, hasReasoning, false))
	}
	return aggregateResponseSemantics(semantics)
}

func classifyResponsesResponse(root gjson.Result, semantics ResponseSemantics) ResponseSemantics {
	response := root
	if root.Get("response").IsObject() && !root.Get("status").Exists() {
		response = root.Get("response")
	}
	if response.Get("usage").IsObject() {
		semantics.Response.UsageState = ResponseUsageUpstream
	}
	status := strings.ToLower(strings.TrimSpace(response.Get("status").String()))
	reason := strings.TrimSpace(response.Get("incomplete_details.reason").String())
	for index, item := range response.Get("output").Array() {
		hasText, hasTool, hasReasoning, hasRefusal := false, false, false, false
		switch strings.ToLower(item.Get("type").String()) {
		case "function_call", "custom_tool_call", "computer_call", "file_search_call", "web_search_call", "code_interpreter_call":
			hasTool = true
		case "reasoning":
			hasReasoning = true
		case "refusal":
			hasRefusal = true
		case "message":
			for _, content := range item.Get("content").Array() {
				switch strings.ToLower(content.Get("type").String()) {
				case "output_text", "text":
					hasText = hasText || hasTextValue(content.Get("text"))
				case "refusal":
					hasRefusal = true
				}
			}
		}
		itemReason := reason
		if itemReason == "" {
			itemReason = status
		}
		semanticItem := classifySemanticItem(index, itemReason, hasText, hasTool, hasReasoning, hasRefusal)
		if status == "incomplete" {
			semanticItem.Incomplete = true
		}
		if status == "failed" || status == "cancelled" {
			semanticItem.Failed = true
		}
		semanticItem.PrimaryOutcome = semanticItemOutcome(semanticItem)
		semantics.Response.Items = append(semantics.Response.Items, semanticItem)
	}
	if len(semantics.Response.Items) == 0 && status != "" {
		item := classifySemanticItem(0, firstNonEmpty(reason, status), false, false, false, false)
		if status == "incomplete" {
			item.Incomplete = true
		}
		if status == "failed" || status == "cancelled" {
			item.Failed = true
		}
		item.PrimaryOutcome = semanticItemOutcome(item)
		semantics.Response.Items = append(semantics.Response.Items, item)
	}
	return aggregateResponseSemantics(semantics)
}

func classifySemanticItem(index int, providerReason string, hasText, hasTool, hasReasoning, explicitRefusal bool) ResponseSemanticItem {
	normalizedReason := normalizeTerminationReason(providerReason)
	if explicitRefusal && !isRejectionReason(normalizedReason) {
		normalizedReason = "content_filter"
	}
	item := ResponseSemanticItem{
		Index:            index,
		PrimaryOutcome:   outcomeFromOutput(hasText, hasTool, hasReasoning),
		RejectionState:   ResponseRejectionNone,
		OutputState:      outputState(hasText, hasTool, hasReasoning),
		ProviderReason:   strings.TrimSpace(providerReason),
		NormalizedReason: normalizedReason,
		HasText:          hasText,
		HasReasoning:     hasReasoning,
		HasToolCalls:     hasTool,
	}
	if explicitRefusal || isRejectionReason(normalizedReason) {
		item.RejectionState = ResponseRejectionAll
	}
	if isFailureReason(normalizedReason) {
		item.Failed = true
	}
	if isTruncationReason(normalizedReason) {
		item.Truncated = true
	}
	item.PrimaryOutcome = semanticItemOutcome(item)
	return item
}

// mergeResponseSemanticItems combines provider-native and converted facts without
// treating the converted representation as another logical candidate.
func mergeResponseSemanticItems(base, additional []ResponseSemanticItem) []ResponseSemanticItem {
	if len(base) == 0 {
		return append([]ResponseSemanticItem(nil), additional...)
	}
	result := append([]ResponseSemanticItem(nil), base...)
	if len(additional) == 0 {
		return result
	}

	used := make([]bool, len(additional))
	for basePosition := range result {
		additionalPosition := -1
		for candidatePosition := range additional {
			if !used[candidatePosition] && additional[candidatePosition].Index == result[basePosition].Index {
				additionalPosition = candidatePosition
				break
			}
		}
		if additionalPosition == -1 && basePosition < len(additional) && !used[basePosition] {
			additionalPosition = basePosition
		}
		if additionalPosition == -1 {
			continue
		}

		used[additionalPosition] = true
		additionalItem := additional[additionalPosition]
		item := &result[basePosition]
		item.HasText = item.HasText || additionalItem.HasText
		item.HasReasoning = item.HasReasoning || additionalItem.HasReasoning
		item.HasToolCalls = item.HasToolCalls || additionalItem.HasToolCalls
		item.Incomplete = item.Incomplete || additionalItem.Incomplete
		item.Failed = item.Failed || additionalItem.Failed
		item.Truncated = item.Truncated || additionalItem.Truncated
		if additionalItem.RejectionState == ResponseRejectionAll {
			item.RejectionState = ResponseRejectionAll
		}
		if item.ProviderReason == "" {
			item.ProviderReason = additionalItem.ProviderReason
		}
		if item.NormalizedReason == "" {
			item.NormalizedReason = additionalItem.NormalizedReason
		}
		item.OutputState = outputState(item.HasText, item.HasToolCalls, item.HasReasoning)
		item.PrimaryOutcome = semanticItemOutcome(*item)
	}
	return result
}

// semanticItemOutcome applies the same precedence used by the response aggregate.
func semanticItemOutcome(item ResponseSemanticItem) string {
	switch {
	case item.RejectionState == ResponseRejectionAll:
		return ResponseOutcomeRejected
	case item.Failed && !item.HasText && !item.HasToolCalls:
		return ResponseOutcomeFailed
	case item.HasToolCalls:
		return ResponseOutcomeToolCall
	case item.Truncated || item.Incomplete:
		return ResponseOutcomeIncomplete
	case item.HasText:
		return ResponseOutcomeCompleted
	default:
		return ResponseOutcomeUnknown
	}
}

func aggregateResponseSemantics(semantics ResponseSemantics) ResponseSemantics {
	response := &semantics.Response
	response.HasText, response.HasReasoning, response.HasToolCalls, response.Truncated = false, false, false, false
	response.RejectionState = ResponseRejectionNone
	response.TransportStatus = transportStatus(semantics.Upstream.HTTPStatus)
	if len(response.Items) == 0 {
		response.PrimaryOutcome = ResponseOutcomeUnknown
		response.OutputState = ResponseOutputEmpty
		return semantics
	}

	rejected, rejectionCandidates, failed, incomplete := 0, 0, 0, 0
	reasons, normalizedReasons := make(map[string]struct{}), make(map[string]struct{})
	for _, item := range response.Items {
		response.HasText = response.HasText || item.HasText
		response.HasReasoning = response.HasReasoning || item.HasReasoning
		response.HasToolCalls = response.HasToolCalls || item.HasToolCalls
		response.Truncated = response.Truncated || item.Truncated
		if countsTowardResponseRejection(semantics.Upstream.Format, item) {
			rejectionCandidates++
			if item.RejectionState == ResponseRejectionAll {
				rejected++
			}
		}
		if item.Failed {
			failed++
		}
		if item.Incomplete {
			incomplete++
		}
		if item.ProviderReason != "" {
			reasons[item.ProviderReason] = struct{}{}
		}
		if item.NormalizedReason != "" {
			normalizedReasons[item.NormalizedReason] = struct{}{}
		}
	}
	switch {
	case rejectionCandidates > 0 && rejected == rejectionCandidates:
		response.RejectionState = ResponseRejectionAll
	case rejected > 0:
		response.RejectionState = ResponseRejectionPartial
	}
	response.OutputState = outputState(response.HasText, response.HasToolCalls, response.HasReasoning)
	// Aggregate order is contractual: all rejected, no-deliverable failed, tools, truncated, text, unknown.
	switch {
	case response.RejectionState == ResponseRejectionAll:
		response.PrimaryOutcome = ResponseOutcomeRejected
	case failed > 0 && !response.HasText && !response.HasToolCalls:
		response.PrimaryOutcome = ResponseOutcomeFailed
	case response.HasToolCalls:
		response.PrimaryOutcome = ResponseOutcomeToolCall
	case response.Truncated || incomplete > 0:
		response.PrimaryOutcome = ResponseOutcomeIncomplete
	case response.HasText:
		response.PrimaryOutcome = ResponseOutcomeCompleted
	default:
		response.PrimaryOutcome = ResponseOutcomeUnknown
	}
	response.ProviderReason = singleReason(reasons)
	response.NormalizedReason = singleReason(normalizedReasons)
	return semantics
}

// countsTowardResponseRejection excludes Responses reasoning metadata from the
// logical output population while retaining it as an independently auditable item.
func countsTowardResponseRejection(format types.RelayFormat, item ResponseSemanticItem) bool {
	if format != types.RelayFormatOpenAIResponses && format != types.RelayFormatOpenAIResponsesCompaction {
		return true
	}
	return item.OutputState != ResponseOutputReasoningOnly ||
		item.RejectionState != ResponseRejectionNone ||
		item.Failed || item.Incomplete || item.Truncated
}

func transportStatus(status int) string {
	switch {
	case status >= 200 && status <= 299:
		return ResponseTransportSuccess
	case status >= 100:
		return ResponseTransportError
	default:
		return ResponseTransportUnknown
	}
}

func outcomeFromOutput(hasText, hasTool, hasReasoning bool) string {
	if hasTool {
		return ResponseOutcomeToolCall
	}
	if hasText {
		return ResponseOutcomeCompleted
	}
	return ResponseOutcomeUnknown
}

func outputState(hasText, hasTool, hasReasoning bool) string {
	switch {
	case hasText && hasTool:
		return ResponseOutputTextAndToolCalls
	case hasTool:
		return ResponseOutputToolCalls
	case hasText:
		return ResponseOutputText
	case hasReasoning:
		return ResponseOutputReasoningOnly
	default:
		return ResponseOutputEmpty
	}
}

func normalizeTerminationReason(reason string) string {
	reason = strings.NewReplacer("-", "_", " ", "_").Replace(strings.ToLower(strings.TrimSpace(reason)))
	switch reason {
	case "max_tokens", "max_output_tokens", "length":
		return "max_tokens"
	case "tool_calls", "function_call", "tool_use":
		return "tool_call"
	case "content_filter", "refusal", "safety", "blocked", "blocklist", "prohibited_content", "spii":
		return "content_filter"
	default:
		return reason
	}
}

func isRejectionReason(reason string) bool {
	return reason == "content_filter" || reason == "recitation"
}

func isFailureReason(reason string) bool {
	switch reason {
	case "malformed_function_call", "unexpected_tool_call", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func isTruncationReason(reason string) bool { return reason == "max_tokens" }

func hasTextValue(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	if value.Type == gjson.String {
		return strings.TrimSpace(value.String()) != ""
	}
	if value.IsArray() {
		for _, item := range value.Array() {
			if hasTextValue(item.Get("text")) || hasTextValue(item.Get("content")) {
				return true
			}
		}
	}
	return false
}

func meaningfulJSON(value gjson.Result) bool {
	if !value.Exists() || value.Type == gjson.Null {
		return false
	}
	if value.Type == gjson.String {
		return strings.TrimSpace(value.String()) != ""
	}
	if value.IsArray() || value.IsObject() {
		trimmed := strings.TrimSpace(value.Raw)
		return trimmed != "[]" && trimmed != "{}"
	}
	return true
}

func nonEmptyArray(value gjson.Result) bool { return value.IsArray() && len(value.Array()) > 0 }

func singleReason(reasons map[string]struct{}) string {
	if len(reasons) == 0 {
		return ""
	}
	if len(reasons) > 1 {
		return "mixed"
	}
	for reason := range reasons {
		return reason
	}
	return ""
}

func mergeUsageState(left, right string) string {
	if left == ResponseUsageUpstream || right == ResponseUsageUpstream {
		return ResponseUsageUpstream
	}
	if left == ResponseUsageEstimated || right == ResponseUsageEstimated {
		return ResponseUsageEstimated
	}
	return ResponseUsageAbsent
}

func mergeStreamState(left, right string) string {
	rank := func(value string) int {
		switch value {
		case ResponseStreamError:
			return 4
		case ResponseStreamIncomplete:
			return 3
		case ResponseStreamComplete:
			return 2
		case ResponseStreamNotStreamed:
			return 1
		default:
			return 0
		}
	}
	if rank(right) > rank(left) {
		return right
	}
	return left
}

func isZeroResponseSemantics(value ResponseSemantics) bool {
	return value.Response.PrimaryOutcome == "" && value.Upstream.Format == "" && value.Client.Format == ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
