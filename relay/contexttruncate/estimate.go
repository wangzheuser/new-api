package contexttruncate

import (
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/tiktoken-go/tokenizer/codec"
)

var contextTokenizer = codec.NewCl100kBase()

// Estimate counts the complete serialized context, not the billing DTO's text subset.
// CL100K is a local fallback, not an assertion about the upstream model's tokenizer.
func Estimate(body []byte, mediaTokens int) (int, error) {
	var root map[string]any
	if err := common.Unmarshal(body, &root); err != nil {
		return 0, err
	}
	// Mask only protocol media positions; tool arguments and schemas remain intact.
	for _, field := range []string{"messages", "contents", "input"} {
		items, _ := root[field].([]any)
		for _, item := range items {
			message, _ := item.(map[string]any)
			maskContextImages(message["content"])
			maskContextImages(message["parts"])
		}
		if field == "input" {
			maskContextImages(items)
		}
	}
	maskContextImages(root["system"])
	for _, field := range []string{"systemInstruction", "system_instruction"} {
		instruction, _ := root[field].(map[string]any)
		maskContextImages(instruction["parts"])
	}
	// Generation controls are reserved by Budget, not charged as input context.
	for _, field := range []string{"model", "stream", "stream_options", "max_tokens", "max_completion_tokens", "max_output_tokens"} {
		delete(root, field)
	}
	encoded, err := common.Marshal(root)
	if err != nil {
		return 0, err
	}
	// Bound tokenizer work on long code identifiers; UTF-8 boundaries stay intact.
	tokens := max(mediaTokens, 0)
	for len(encoded) > 0 {
		n := min(len(encoded), 2048)
		for n < len(encoded) && !utf8.RuneStart(encoded[n]) {
			n--
		}
		count, err := contextTokenizer.Count(string(encoded[:n]))
		if err != nil {
			return 0, err
		}
		tokens += count
		encoded = encoded[n:]
	}
	return tokens, nil
}

// maskContextImages excludes binary transport data without omitting adjacent text or tool inputs.
func maskContextImages(value any) {
	blocks, _ := value.([]any)
	for _, value := range blocks {
		block, _ := value.(map[string]any)
		switch block["type"] {
		case "image", "image_url", "input_image":
			delete(block, "source")
			delete(block, "image_url")
		case "tool_result":
			maskContextImages(block["content"])
		}
		for _, key := range []string{"inlineData", "inline_data", "fileData", "file_data"} {
			if _, exists := block[key]; exists {
				block[key] = map[string]any{"mimeType": "image"}
			}
		}
		for _, key := range []string{"functionResponse", "function_response"} {
			result, _ := block[key].(map[string]any)
			maskContextImages(result["parts"])
		}
	}
}
