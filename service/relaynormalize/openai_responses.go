package relaynormalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// normalizeOpenAIResponsesCompatible keeps Responses call references valid for Claude-compatible upstreams.
func normalizeOpenAIResponsesCompatible(body []byte) ([]byte, types.ProtocolNormalizationAudit, error) {
	audit := types.ProtocolNormalizationAudit{Normalizer: RequestNormalizerOpenAIResponsesCompatible}
	root, input, err := parseOpenAIResponsesBody(body)
	if err != nil {
		return nil, audit, err
	}
	if input == nil {
		return body, audit, nil
	}

	normalizer := NewClaudeToolIDNormalizer()
	callIDs := make(map[string]struct{})
	for _, itemRaw := range input {
		item, itemType, itemErr := parseResponsesInputItem(itemRaw)
		if itemErr != nil {
			return nil, audit, itemErr
		}
		callID, usesCallID, callIDErr := responsesCallIDField(item, itemType)
		if callIDErr != nil {
			return nil, audit, callIDErr
		}
		if !usesCallID || isResponsesCallOutputItem(itemType) {
			continue
		}
		callIDs[callID] = struct{}{}
		normalizer.Normalize(callID)
	}

	for index, itemRaw := range input {
		item, itemType, itemErr := parseResponsesInputItem(itemRaw)
		if itemErr != nil {
			return nil, audit, itemErr
		}
		callID, usesCallID, callIDErr := responsesCallIDField(item, itemType)
		if callIDErr != nil {
			return nil, audit, callIDErr
		}
		if !usesCallID {
			continue
		}
		if isResponsesCallOutputItem(itemType) {
			if _, found := callIDs[callID]; !found {
				audit.OrphanToolResultIDs++
			}
		}
		normalized, changed, _ := normalizer.Normalize(callID)
		if changed {
			audit.ToolIDsNormalized++
		}
		item["call_id"], err = common.Marshal(normalized)
		if err != nil {
			return nil, audit, fmt.Errorf("marshal normalized responses call_id: %w", err)
		}
		input[index], err = common.Marshal(item)
		if err != nil {
			return nil, audit, fmt.Errorf("marshal responses input item: %w", err)
		}
	}

	audit.ToolIDCollisions = normalizer.Collisions()
	root["input"], err = common.Marshal(input)
	if err != nil {
		return nil, audit, fmt.Errorf("marshal normalized responses input: %w", err)
	}
	normalizedBody, err := common.Marshal(root)
	if err != nil {
		return nil, audit, fmt.Errorf("marshal normalized responses request: %w", err)
	}
	return normalizedBody, audit, nil
}

// validateOpenAIResponsesCompatible verifies every tool call reference in the final wire body.
func validateOpenAIResponsesCompatible(body []byte) error {
	_, input, err := parseOpenAIResponsesBody(body)
	if err != nil || input == nil {
		return err
	}
	for _, itemRaw := range input {
		item, itemType, itemErr := parseResponsesInputItem(itemRaw)
		if itemErr != nil {
			return itemErr
		}
		callID, usesCallID, callIDErr := responsesCallIDField(item, itemType)
		if callIDErr != nil {
			return callIDErr
		}
		if !usesCallID {
			continue
		}
		if !claudeToolIDPattern.MatchString(callID) {
			return fmt.Errorf("responses call_id does not match ^[A-Za-z0-9_-]+$")
		}
	}
	return nil
}

// parseOpenAIResponsesBody decodes the request while preserving unmodified JSON values exactly.
func parseOpenAIResponsesBody(body []byte) (map[string]json.RawMessage, []json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := common.Unmarshal(body, &root); err != nil {
		return nil, nil, fmt.Errorf("invalid responses request body: %w", err)
	}
	if root == nil {
		return nil, nil, fmt.Errorf("responses request body must be a JSON object")
	}
	inputRaw, exists := root["input"]
	if !exists || firstJSONByte(inputRaw) != '[' {
		return root, nil, nil
	}
	var input []json.RawMessage
	if err := common.Unmarshal(inputRaw, &input); err != nil {
		return nil, nil, fmt.Errorf("responses input must be an array: %w", err)
	}
	return root, input, nil
}

// parseResponsesInputItem extracts object input items and leaves scalar inputs untouched.
func parseResponsesInputItem(itemRaw json.RawMessage) (map[string]json.RawMessage, string, error) {
	if firstJSONByte(itemRaw) != '{' {
		return nil, "", nil
	}
	item, err := decodeJSONObject(itemRaw, "responses input item")
	if err != nil {
		return nil, "", err
	}
	var itemType string
	if rawType, exists := item["type"]; exists {
		if err := common.Unmarshal(rawType, &itemType); err != nil {
			return nil, "", fmt.Errorf("responses input item type must be a string: %w", err)
		}
	}
	return item, itemType, nil
}

// responsesCallIDField reads one Responses tool reference without coercing its JSON type.
func responsesCallIDField(item map[string]json.RawMessage, itemType string) (string, bool, error) {
	if item == nil {
		return "", false, nil
	}
	raw, exists := item["call_id"]
	if !exists {
		// Function and custom tool items require call_id; normalize a missing value
		// like an empty identifier so the final wire body remains valid.
		switch itemType {
		case "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output":
			return "", true, nil
		default:
			return "", false, nil
		}
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", true, nil
	}
	var value string
	if err := common.Unmarshal(raw, &value); err != nil {
		return "", true, fmt.Errorf("responses call_id must be a string: %w", err)
	}
	return value, true, nil
}

// isResponsesCallOutputItem reports whether an input item references a prior tool call.
func isResponsesCallOutputItem(itemType string) bool {
	return strings.HasSuffix(itemType, "_call_output") || itemType == "mcp_approval_response"
}
