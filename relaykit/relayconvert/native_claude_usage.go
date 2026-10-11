package relayconvert

import "github.com/QuantumNous/new-api/dto"

// nativeClaudeStreamUsage merges sparse message_delta usage without changing client frames.
func (s *ResponseStreamState) nativeClaudeStreamUsage(response any) *dto.Usage {
	chunk, err := asClaudeResponse(response)
	if err != nil || (chunk.Type != "message_start" && chunk.Type != "message_delta") {
		return s.usage
	}
	if s.nativeClaudeUsage == nil {
		s.nativeClaudeUsage = &ClaudeResponseInfo{}
	}
	// Only usage-bearing events go through the existing accumulator; do not buffer response text twice.
	FormatClaudeResponseInfo(chunk, nil, s.nativeClaudeUsage)
	usage := s.nativeClaudeUsage.Usage
	if usage != nil {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage
}
