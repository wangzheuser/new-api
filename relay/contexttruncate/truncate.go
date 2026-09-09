// Package contexttruncate trims whole conversational turns without modifying content blocks.
package contexttruncate

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/tidwall/gjson"
	"math"
	"strings"
)

// Result retains only accounting and boundary facts, never deleted message text.
type Result struct {
	Before          int
	After           int
	RemovedTurns    int
	RemovedMessages int
	Applied         bool
	BypassReason    string
}

// Shape identifies the message array and excludes incomplete or multimodal histories.
func Shape(body []byte) (field, reason string) {
	return shape(body, false)
}

// shape keeps cache eligibility text-only while allowing independently validated image histories.
func shape(body []byte, images bool) (field, reason string) {
	root := gjson.ParseBytes(body)
	for _, path := range []string{"modalities", "generationConfig.responseModalities", "generation_config.response_modalities"} {
		for _, modality := range root.Get(path).Array() {
			if !strings.EqualFold(modality.String(), "text") {
				return "", "multimodal"
			}
		}
	}
	for _, k := range []string{"previous_response_id", "conversation", "cachedContent", "cached_content"} {
		v := root.Get(k)
		if v.Exists() && v.Type != gjson.Null && v.Raw != `""` {
			reason = "provider_state_reference"
		}
	}
	switch {
	case root.Get("messages").IsArray():
		field = "messages"
	case root.Get("contents").IsArray():
		field = "contents"
	case root.Get("input").IsArray():
		field = "input"
	case root.Get("input").Type == gjson.String:
		return "input", reason
	default:
		return "", "unsupported_request"
	}
	for _, m := range root.Get(field).Array() {
		if m.Get("audio").Exists() {
			return "", "multimodal"
		}
		kind := m.Get("type").String()
		if (field == "input" && kind != "" && kind != "message" && kind != "function_call" && kind != "function_call_output" && kind != "reasoning") || m.Get("encrypted_content").Exists() {
			return "", "opaque_history"
		}
		if m.Get("function_call").Exists() || m.Get("role").String() == "function" {
			return "", "legacy_tool_history"
		}
		blocks := m.Get("content")
		if field == "contents" {
			blocks = m.Get("parts")
		}
		if images {
			if reason := imageHistoryBlocks(blocks, field == "contents"); reason != "" {
				return "", reason
			}
			continue
		}
		for _, b := range blocks.Array() {
			t := b.Get("type").String()
			if t == "image" || t == "image_url" || t == "input_image" || t == "input_audio" || t == "audio" || t == "file" || t == "document" || t == "input_file" || b.Get("inlineData").Exists() || b.Get("fileData").Exists() || b.Get("inline_data").Exists() || b.Get("file_data").Exists() {
				return "", "multimodal"
			}
			if field != "contents" {
				switch t {
				case "", "text", "input_text", "output_text", "tool_use", "tool_result", "thinking", "redacted_thinking", "server_tool_use", "web_search_tool_result", "refusal":
				default:
					return "", "opaque_history"
				}
			}
			if t == "tool_result" {
				for _, v := range b.Get("content").Array() {
					if k := v.Get("type").String(); k != "text" && k != "" {
						return "", "multimodal"
					}
				}
			}
		}
	}
	return field, reason
}

// Trim removes oldest dependency-connected turn groups. Count measures the complete candidate body.
func Trim(ctx context.Context, body []byte, budget, keep int, count func([]byte) (int, error)) ([]byte, Result, error) {
	result := Result{}
	field, reason := TruncationShape(body)
	if reason != "" {
		result.BypassReason = reason
		if reason != "provider_state_reference" {
			return nil, result, fmt.Errorf("context_truncation_unsupported_content: %s", reason)
		}
		return body, result, nil
	}
	if budget <= 0 || keep < 1 {
		return nil, result, fmt.Errorf("context_length_exceeded")
	}
	before, err := count(body)
	if err != nil {
		return nil, result, err
	}
	result.Before = before
	result.After = before
	if before <= budget {
		return body, result, nil
	}
	var root map[string]json.RawMessage
	if err = common.Unmarshal(body, &root); err != nil {
		return nil, result, err
	}
	if !gjson.ParseBytes(root[field]).IsArray() {
		return nil, result, fmt.Errorf("context_length_exceeded")
	}
	var messages []json.RawMessage
	if err = common.Unmarshal(root[field], &messages); err != nil {
		return nil, result, err
	}
	turns := make([]int, len(messages))
	parent := []int{}
	pinned := map[int]bool{}
	current := -1
	calls := map[string][]int{}
	pairs := [][2]int{}
	for i, raw := range messages {
		m := gjson.ParseBytes(raw)
		role := m.Get("role").String()
		kind := m.Get("type").String()
		refs, results := messageTools(m, field)
		if role == "system" || role == "developer" {
			turns[i] = -1
			continue
		}
		if current < 0 || (role == "user" && len(results) == 0) {
			current++
			parent = append(parent, current)
		}
		turns[i] = current
		if kind == "reasoning" {
			pinned[current] = true
		} // Signed/opaque reasoning is kept intact with its dependency group.
		for _, id := range refs {
			if id == "" {
				return nil, result, fmt.Errorf("context_truncation_invalid_tool_reference")
			}
			calls[id] = append(calls[id], current)
		}
		for _, id := range results {
			stack := calls[id]
			if len(stack) == 0 {
				return nil, result, fmt.Errorf("context_truncation_invalid_tool_reference")
			}
			pairs = append(pairs, [2]int{stack[0], current})
			calls[id] = stack[1:]
		}
	}
	for _, stack := range calls {
		for _, turn := range stack {
			pinned[turn] = true
		}
	}
	for _, pair := range pairs {
		a, b := find(parent, pair[0]), find(parent, pair[1])
		parent[b] = a
	}
	protected := map[int]bool{}
	for t := max(0, len(parent)-keep); t < len(parent); t++ {
		protected[find(parent, t)] = true
	}
	for t := range pinned {
		protected[find(parent, t)] = true
	}
	groups := []int{}
	seen := map[int]bool{}
	for t := range parent {
		r := find(parent, t)
		if !protected[r] && !seen[r] {
			groups = append(groups, r)
			seen[r] = true
		}
	}
	if len(groups) == 0 {
		return nil, result, fmt.Errorf("context_length_exceeded")
	}
	ranks := map[int]int{}
	for rank, g := range groups {
		ranks[g] = rank
	}
	// Binary search avoids re-counting the full prompt once per deleted message.
	low, high := 1, len(groups)
	var selected []byte
	var removedMessages, removedTurns int
	for low <= high {
		if err := ctx.Err(); err != nil {
			return nil, result, err
		}
		n := (low + high) / 2
		candidate := make([]json.RawMessage, 0, len(messages))
		deletedTurns := map[int]bool{}
		for i, m := range messages {
			t := turns[i]
			if t >= 0 {
				r := find(parent, t)
				rank, ok := ranks[r]
				if ok && rank < n {
					deletedTurns[t] = true
					continue
				}
			}
			candidate = append(candidate, m)
		}
		root[field], err = common.Marshal(candidate)
		if err != nil {
			return nil, result, err
		}
		encoded, e := common.Marshal(root)
		if e != nil {
			return nil, result, e
		}
		tokens, e := count(encoded)
		if e != nil {
			return nil, result, e
		}
		if tokens <= budget {
			selected = encoded
			result.After = tokens
			removedMessages = len(messages) - len(candidate)
			removedTurns = len(deletedTurns)
			high = n - 1
		} else {
			low = n + 1
		}
	}
	if selected == nil {
		return nil, result, fmt.Errorf("context_length_exceeded")
	}
	result.Applied = true
	result.RemovedMessages = removedMessages
	result.RemovedTurns = removedTurns
	return selected, result, nil
}

// find resolves a turn's dependency component with path compression.
func find(parent []int, n int) int {
	for parent[n] != n {
		parent[n] = parent[parent[n]]
		n = parent[n]
	}
	return n
}

// messageTools extracts protocol-native references without reading tool arguments as messages.
func messageTools(m gjson.Result, field string) (calls, results []string) {
	for _, t := range m.Get("tool_calls").Array() {
		calls = append(calls, t.Get("id").String())
	}
	if m.Get("role").String() == "tool" {
		results = append(results, m.Get("tool_call_id").String())
	}
	switch m.Get("type").String() {
	case "function_call":
		calls = append(calls, m.Get("call_id").String())
	case "function_call_output":
		results = append(results, m.Get("call_id").String())
	}
	blocks := m.Get("content")
	if field == "contents" {
		blocks = m.Get("parts")
	}
	for _, b := range blocks.Array() {
		switch b.Get("type").String() {
		case "tool_use", "server_tool_use":
			calls = append(calls, b.Get("id").String())
		case "tool_result", "web_search_tool_result":
			results = append(results, b.Get("tool_use_id").String())
		}
		for _, pair := range [][2]string{{"functionCall", "functionResponse"}, {"function_call", "function_response"}} {
			if v := b.Get(pair[0]); v.Exists() {
				id := v.Get("id").String()
				if id == "" {
					id = "gemini:" + v.Get("name").String()
				}
				calls = append(calls, id)
			}
			if v := b.Get(pair[1]); v.Exists() {
				id := v.Get("id").String()
				if id == "" {
					id = "gemini:" + v.Get("name").String()
				}
				results = append(results, id)
			}
		}
	}
	return
}

// isOutputLimitWrite recognizes bounded scalar assignments, not whole-object mutations.
func isOutputLimitWrite(path string, value gjson.Result) bool {
	switch path {
	case "max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig.maxOutputTokens":
		n := value.Float()
		return value.Type == gjson.Number && n > 0 && n <= dto.MaxOutputTokens && n == math.Trunc(n)
	default:
		return false
	}
}

// Conflicts protects content and permits output assignments checked again on the final body.
func Conflicts(overrides map[string]interface{}) bool {
	keys := []string{"messages", "input", "contents", "system", "system_instruction", "systemInstruction", "instructions", "tools", "max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig", "generation_config", "model"}
	encoded, _ := common.Marshal(overrides)
	var walk func(gjson.Result) bool
	walk = func(v gjson.Result) bool {
		if phase := v.Get("phase").String(); phase != "" && phase != "request" {
			return false
		}
		if mode := v.Get("mode").String(); mode != "" {
			if mode == "set" && isOutputLimitWrite(v.Get("path").String(), v.Get("value")) {
				return false
			}
			switch mode {
			case "return_error", "set_header", "delete_header", "copy_header", "move_header", "pass_headers":
				return false
			}
			if v.Get("path").Exists() && v.Get("path").String() == "" {
				return true
			}
		}
		found := false
		v.ForEach(func(k, x gjson.Result) bool {
			key := k.String()
			if key == "conditions" {
				return true
			}
			if isOutputLimitWrite(key, x) {
				return true
			}
			for _, protected := range keys {
				if key == protected || len(key) > len(protected) && key[:len(protected)] == protected && (key[len(protected)] == '.' || key[len(protected)] == '[') {
					found = true
					return false
				}
			}
			if key == "path" || key == "from" || key == "to" {
				parts := strings.TrimPrefix(x.String(), "/")
				if strings.ContainsAny(parts, "*?") {
					found = true
					return false
				}
				for _, protected := range keys {
					if parts == protected || strings.HasPrefix(parts, protected+".") || strings.HasPrefix(parts, protected+"[") || strings.HasPrefix(parts, protected+"/") {
						found = true
						return false
					}
				}
			}
			if x.IsObject() || x.IsArray() {
				found = walk(x)
			}
			return !found
		})
		return found
	}
	// Stable traversal is not required: this check returns a boolean, never a mutation order.
	return walk(gjson.ParseBytes(encoded))
}
