package service

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// TestSanitizeConversationBody verifies text is preserved while JSON and SSE media payloads are omitted.
func TestSanitizeConversationBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		changed     bool
		contains    string
		notContains string
	}{
		{name: "plain json", body: `{"messages":[{"content":"hello"}]}`, changed: false, contains: `"hello"`},
		{name: "data uri", body: `{"image_url":{"url":"data:image/png;base64,aGVsbG8="},"text":"hello"}`, changed: true, contains: conversationBinaryOmitted, notContains: "aGVsbG8="},
		{name: "truncated data uri", body: `{"url":"data:image/png;base64,aGVsbG8=`, changed: true, contains: conversationBinaryOmitted, notContains: "aGVsbG8="},
		{name: "claude base64", body: `{"source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}`, changed: true, contains: conversationBinaryOmitted, notContains: "aGVsbG8="},
		{name: "gemini sse", body: "data: {\"candidates\":[],\"inlineData\":{\"mimeType\":\"image/png\",\"data\":\"aGVsbG8=\"}}\n\n", changed: true, contains: conversationBinaryOmitted, notContains: "aGVsbG8="},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sanitized, changed := sanitizeConversationBody([]byte(test.body))
			assert.Equal(t, test.changed, changed)
			assert.Contains(t, string(sanitized), test.contains)
			if test.notContains != "" {
				assert.NotContains(t, string(sanitized), test.notContains)
			}
		})
	}
}

// TestSanitizeConversationBodyRepairsInvalidUTF8 protects conversation log persistence on PostgreSQL.
func TestSanitizeConversationBodyRepairsInvalidUTF8(t *testing.T) {
	sanitized, changed := sanitizeConversationBody([]byte{'h', 'i', 0xe4})

	assert.False(t, changed)
	assert.True(t, utf8.Valid(sanitized))
	assert.Equal(t, "hi\uFFFD", string(sanitized))
}
