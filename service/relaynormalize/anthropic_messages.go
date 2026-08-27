package relaynormalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

var claudeToolIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func normalizeAnthropicMessagesCompatible(body []byte) ([]byte, types.ProtocolNormalizationAudit, error) {
	audit := types.ProtocolNormalizationAudit{Normalizer: RequestNormalizerAnthropicMessagesCompatible}
	root, messages, err := parseAnthropicMessagesBody(body)
	if err != nil {
		return nil, audit, err
	}

	normalizer := NewClaudeToolIDNormalizer()
	toolUseIDs := make(map[string]struct{})
	for _, messageRaw := range messages {
		blocks, parseErr := parseClaudeContentBlocks(messageRaw)
		if parseErr != nil {
			return nil, audit, parseErr
		}
		for _, blockRaw := range blocks {
			block, blockType, blockErr := parseClaudeContentBlock(blockRaw)
			if blockErr != nil {
				return nil, audit, blockErr
			}
			if blockType != "tool_use" {
				continue
			}
			id, idErr := claudeToolIDField(block, "id")
			if idErr != nil {
				return nil, audit, idErr
			}
			toolUseIDs[id] = struct{}{}
			normalizer.Normalize(id)
		}
	}

	normalizedMessages := make([]json.RawMessage, 0, len(messages))
	for _, messageRaw := range messages {
		drop, dropErr := isReasoningOnlyAssistantMessage(messageRaw)
		if dropErr != nil {
			return nil, audit, dropErr
		}
		if drop {
			audit.ReasoningOnlyAssistantDropped++
			continue
		}

		message, messageErr := decodeJSONObject(messageRaw, "claude message")
		if messageErr != nil {
			return nil, audit, messageErr
		}
		contentRaw, exists := message["content"]
		if !exists || firstJSONByte(contentRaw) != '[' {
			normalizedMessages = append(normalizedMessages, messageRaw)
			continue
		}
		var blocks []json.RawMessage
		if err := common.Unmarshal(contentRaw, &blocks); err != nil {
			return nil, audit, fmt.Errorf("invalid claude message content: %w", err)
		}
		for index, blockRaw := range blocks {
			block, blockType, blockErr := parseClaudeContentBlock(blockRaw)
			if blockErr != nil {
				return nil, audit, blockErr
			}
			field := ""
			switch blockType {
			case "tool_use":
				field = "id"
			case "tool_result":
				field = "tool_use_id"
			}
			if field == "" {
				continue
			}
			original, idErr := claudeToolIDField(block, field)
			if idErr != nil {
				return nil, audit, idErr
			}
			if blockType == "tool_result" {
				if _, found := toolUseIDs[original]; !found {
					audit.OrphanToolResultIDs++
				}
			}
			normalized, changed, _ := normalizer.Normalize(original)
			if changed {
				audit.ToolIDsNormalized++
			}
			block[field], err = common.Marshal(normalized)
			if err != nil {
				return nil, audit, fmt.Errorf("marshal normalized claude tool id: %w", err)
			}
			blocks[index], err = common.Marshal(block)
			if err != nil {
				return nil, audit, fmt.Errorf("marshal claude content block: %w", err)
			}
		}
		message["content"], err = common.Marshal(blocks)
		if err != nil {
			return nil, audit, fmt.Errorf("marshal claude message content: %w", err)
		}
		normalizedMessage, err := common.Marshal(message)
		if err != nil {
			return nil, audit, fmt.Errorf("marshal claude message: %w", err)
		}
		normalizedMessages = append(normalizedMessages, normalizedMessage)
	}

	audit.ToolIDCollisions = normalizer.Collisions()
	root["messages"], err = common.Marshal(normalizedMessages)
	if err != nil {
		return nil, audit, fmt.Errorf("marshal normalized claude messages: %w", err)
	}
	normalizedBody, err := common.Marshal(root)
	if err != nil {
		return nil, audit, fmt.Errorf("marshal normalized claude request: %w", err)
	}
	return normalizedBody, audit, nil
}

func validateAnthropicMessagesCompatible(body []byte) error {
	_, messages, err := parseAnthropicMessagesBody(body)
	if err != nil {
		return err
	}
	for _, messageRaw := range messages {
		drop, dropErr := isReasoningOnlyAssistantMessage(messageRaw)
		if dropErr != nil {
			return dropErr
		}
		if drop {
			return fmt.Errorf("reasoning-only assistant message remains after normalization")
		}
		blocks, parseErr := parseClaudeContentBlocks(messageRaw)
		if parseErr != nil {
			return parseErr
		}
		for _, blockRaw := range blocks {
			block, blockType, blockErr := parseClaudeContentBlock(blockRaw)
			if blockErr != nil {
				return blockErr
			}
			field := ""
			switch blockType {
			case "tool_use":
				field = "id"
			case "tool_result":
				field = "tool_use_id"
			}
			if field == "" {
				continue
			}
			id, idErr := claudeToolIDField(block, field)
			if idErr != nil {
				return idErr
			}
			if !claudeToolIDPattern.MatchString(id) {
				return fmt.Errorf("claude %s does not match ^[A-Za-z0-9_-]+$", field)
			}
		}
	}
	return nil
}

func parseAnthropicMessagesBody(body []byte) (map[string]json.RawMessage, []json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := common.Unmarshal(body, &root); err != nil {
		return nil, nil, fmt.Errorf("invalid anthropic request body: %w", err)
	}
	if root == nil {
		return nil, nil, fmt.Errorf("anthropic request body must be a JSON object")
	}
	messagesRaw, exists := root["messages"]
	if !exists {
		return root, nil, nil
	}
	var messages []json.RawMessage
	if err := common.Unmarshal(messagesRaw, &messages); err != nil {
		return nil, nil, fmt.Errorf("anthropic messages must be an array: %w", err)
	}
	return root, messages, nil
}

func parseClaudeContentBlocks(messageRaw json.RawMessage) ([]json.RawMessage, error) {
	message, err := decodeJSONObject(messageRaw, "claude message")
	if err != nil {
		return nil, err
	}
	contentRaw, exists := message["content"]
	if !exists || firstJSONByte(contentRaw) != '[' {
		return nil, nil
	}
	var blocks []json.RawMessage
	if err := common.Unmarshal(contentRaw, &blocks); err != nil {
		return nil, fmt.Errorf("invalid claude message content: %w", err)
	}
	return blocks, nil
}

func parseClaudeContentBlock(blockRaw json.RawMessage) (map[string]json.RawMessage, string, error) {
	block, err := decodeJSONObject(blockRaw, "claude content block")
	if err != nil {
		return nil, "", err
	}
	var blockType string
	if rawType, exists := block["type"]; exists {
		if err := common.Unmarshal(rawType, &blockType); err != nil {
			return nil, "", fmt.Errorf("claude content block type must be a string: %w", err)
		}
	}
	return block, blockType, nil
}

func isReasoningOnlyAssistantMessage(messageRaw json.RawMessage) (bool, error) {
	message, err := decodeJSONObject(messageRaw, "claude message")
	if err != nil {
		return false, err
	}
	var role string
	if roleRaw, exists := message["role"]; exists {
		if err := common.Unmarshal(roleRaw, &role); err != nil {
			return false, fmt.Errorf("claude message role must be a string: %w", err)
		}
	}
	if role != "assistant" {
		return false, nil
	}
	blocks, err := parseClaudeContentBlocks(messageRaw)
	if err != nil || len(blocks) == 0 {
		return false, err
	}
	hasReasoning := false
	for _, blockRaw := range blocks {
		_, blockType, blockErr := parseClaudeContentBlock(blockRaw)
		if blockErr != nil {
			return false, blockErr
		}
		switch blockType {
		case "thinking", "redacted_thinking":
			hasReasoning = true
		default:
			return false, nil
		}
	}
	return hasReasoning, nil
}

func claudeToolIDField(block map[string]json.RawMessage, field string) (string, error) {
	raw, exists := block[field]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var value string
	if err := common.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("claude %s must be a string: %w", field, err)
	}
	return value, nil
}

func decodeJSONObject(raw json.RawMessage, name string) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := common.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("%s must be an object: %w", name, err)
	}
	if value == nil {
		return nil, fmt.Errorf("%s must be an object", name)
	}
	return value, nil
}

func firstJSONByte(raw json.RawMessage) byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0
	}
	return trimmed[0]
}
