package helper

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

// StreamPayloadObservation describes a successfully parsed business SSE payload.
type StreamPayloadObservation struct {
	Meaningful        bool
	ToolNameBytes     int
	ToolArgumentBytes int
}

// ObserveStreamDataPayload classifies commit authority and counts flushed tool fragments.
func ObserveStreamDataPayload(data string, relayFormat types.RelayFormat) StreamPayloadObservation {
	observation := StreamPayloadObservation{}
	var payload map[string]interface{}
	if common.UnmarshalJsonStr(data, &payload) != nil {
		return observation
	}
	if _, isError := payload["error"]; isError || payload["type"] == "error" {
		return observation
	}
	if relayFormat == types.RelayFormatClaude {
		if payload["type"] == "content_block_start" {
			block, _ := payload["content_block"].(map[string]interface{})
			name := stringValue(block["name"])
			if block["type"] == "tool_use" && strings.TrimSpace(name) != "" {
				observation.Meaningful = true
				observation.ToolNameBytes = len([]byte(name))
			}
			return observation
		}
		delta, _ := payload["delta"].(map[string]interface{})
		switch delta["type"] {
		case "text_delta":
			observation.Meaningful = strings.TrimSpace(stringValue(delta["text"])) != ""
		case "thinking_delta":
			observation.Meaningful = strings.TrimSpace(stringValue(delta["thinking"])) != ""
		case "input_json_delta":
			arguments := stringValue(delta["partial_json"])
			observation.Meaningful = strings.TrimSpace(arguments) != ""
			observation.ToolArgumentBytes = len([]byte(arguments))
		}
		return observation
	}

	choices, _ := payload["choices"].([]interface{})
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]interface{})
		delta, _ := choice["delta"].(map[string]interface{})
		if hasNonEmptyStreamText(delta["content"]) ||
			hasNonEmptyStreamText(delta["reasoning_content"]) ||
			hasNonEmptyStreamText(delta["reasoning"]) {
			observation.Meaningful = true
		}
		calls, _ := delta["tool_calls"].([]interface{})
		for _, rawCall := range calls {
			call, _ := rawCall.(map[string]interface{})
			function, _ := call["function"].(map[string]interface{})
			name := stringValue(function["name"])
			arguments := stringValue(function["arguments"])
			if strings.TrimSpace(name) != "" || strings.TrimSpace(arguments) != "" {
				observation.Meaningful = true
			}
			observation.ToolNameBytes += len([]byte(name))
			observation.ToolArgumentBytes += len([]byte(arguments))
		}
	}
	return observation
}

func hasNonEmptyStreamText(value interface{}) bool {
	return strings.TrimSpace(stringValue(value)) != ""
}

func stringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}
