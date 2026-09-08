package contexttruncate

import (
	"strings"

	"github.com/tidwall/gjson"
)

// TruncationShape accepts text and explicit images without enabling cache simulation for media.
func TruncationShape(body []byte) (field, reason string) {
	return shape(body, true)
}

// imageHistoryBlocks checks content blocks, including images nested in tool results.
func imageHistoryBlocks(blocks gjson.Result, gemini bool) string {
	if !blocks.Exists() || blocks.Type == gjson.Null || blocks.Type == gjson.String {
		return ""
	}
	if !blocks.IsArray() {
		return "unsupported_content"
	}
	for _, b := range blocks.Array() {
		if !b.IsObject() {
			return "unsupported_content"
		}
		if gemini {
			known := false
			unsupported := false
			b.ForEach(func(key, value gjson.Result) bool {
				switch key.String() {
				case "text", "functionCall", "function_call", "thought", "thoughtSignature", "thought_signature", "mediaResolution", "media_resolution":
					known = true
				case "functionResponse", "function_response":
					known = true
					unsupported = imageHistoryBlocks(value.Get("parts"), true) != ""
				case "inlineData", "inline_data", "fileData", "file_data":
					known = true
					mime := value.Get("mimeType").String()
					if mime == "" {
						mime = value.Get("mime_type").String()
					}
					unsupported = !strings.HasPrefix(strings.ToLower(mime), "image/")
				default:
					unsupported = true
				}
				return !unsupported
			})
			if unsupported || !known {
				return "unsupported_content"
			}
			continue
		}
		switch b.Get("type").String() {
		case "image":
			source := b.Get("source")
			if source.Get("type").String() == "base64" {
				if !strings.HasPrefix(strings.ToLower(source.Get("media_type").String()), "image/") || source.Get("data").String() == "" {
					return "unsupported_content"
				}
			} else if source.Get("type").String() != "url" || source.Get("url").String() == "" {
				return "unsupported_content"
			}
		case "image_url", "input_image":
			url := b.Get("image_url")
			if url.IsObject() {
				url = url.Get("url")
			}
			// Opaque file IDs do not contain enough information for the local image counter.
			if url.Type != gjson.String || url.String() == "" {
				return "unsupported_content"
			}
		case "tool_result":
			if reason := imageHistoryBlocks(b.Get("content"), false); reason != "" {
				return reason
			}
		case "":
			if !b.Get("text").Exists() {
				return "unsupported_content"
			}
		case "text", "input_text", "output_text", "tool_use", "thinking", "redacted_thinking", "server_tool_use", "web_search_tool_result", "refusal":
		case "input_audio", "audio", "file", "document", "input_file", "video", "input_video":
			return "multimodal"
		default:
			return "opaque_history"
		}
	}
	return ""
}
