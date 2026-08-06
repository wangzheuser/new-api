package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGeminiTokenCountMetaIncludesSystemInstruction(t *testing.T) {
	request := &GeminiChatRequest{
		SystemInstructions: &GeminiChatContent{Parts: []GeminiPart{{Text: "system rule"}}},
		Contents:           []GeminiChatContent{{Parts: []GeminiPart{{Text: "user text"}}}},
	}

	meta := request.GetTokenCountMeta()

	assert.Contains(t, meta.CombineText, "system rule")
	assert.Contains(t, meta.CombineText, "user text")
}
