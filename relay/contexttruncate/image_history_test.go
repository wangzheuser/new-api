package contexttruncate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestImageHistoryWholeTurns preserves current images and tool dependencies across protocol shapes.
func TestImageHistoryWholeTurns(t *testing.T) {
	for _, tc := range []struct{ name, field, body string }{
		{"chat", "messages", `{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"old-image"}}]},{"role":"user","content":"latest"},{"role":"assistant","tool_calls":[{"id":"call"}]},{"role":"tool","tool_call_id":"call","content":[{"type":"image_url","image_url":{"url":"current-image"}}]}]}`},
		{"claude", "messages", `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"old-image"}}]},{"role":"user","content":"latest"},{"role":"assistant","content":[{"type":"tool_use","id":"call"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call","content":[{"type":"image","source":{"type":"url","url":"current-image"}}]}]}]}`},
		{"responses", "input", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"old-image"}]},{"role":"user","content":[{"type":"input_image","image_url":"current-image"}]},{"type":"function_call","call_id":"call"},{"type":"function_call_output","call_id":"call","output":"latest"}]}`},
		{"gemini", "contents", `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"old-image"}}]},{"role":"user","parts":[{"text":"latest"}]},{"role":"model","parts":[{"functionCall":{"id":"call","name":"look"}}]},{"role":"user","parts":[{"functionResponse":{"id":"call","name":"look","parts":[{"inlineData":{"mimeType":"image/png","data":"current-image"}}]}}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			count := func(b []byte) (int, error) { return len(gjson.GetBytes(b, tc.field).Array()) * 100, nil }
			_, reason := Shape(body)
			assert.Equal(t, "multimodal", reason, "cache eligibility must remain text-only")
			unchanged, result, err := Trim(context.Background(), body, 400, 1, count)
			require.NoError(t, err)
			assert.Equal(t, body, unchanged)
			assert.False(t, result.Applied)
			got, result, err := Trim(context.Background(), body, 300, 1, count)
			require.NoError(t, err)
			assert.True(t, result.Applied)
			assert.Equal(t, 1, result.RemovedTurns)
			assert.Equal(t, 1, result.RemovedMessages)
			before := gjson.GetBytes(body, tc.field).Array()
			after := gjson.GetBytes(got, tc.field).Array()
			require.Len(t, after, 3)
			for i := range after {
				assert.JSONEq(t, before[i+1].Raw, after[i].Raw)
			}
			_, _, err = Trim(context.Background(), body, 299, 1, count)
			require.ErrorContains(t, err, "context_length_exceeded")
		})
	}
}

// TestUnsupportedHistoryFailsClosed ensures non-image content never silently escapes a selected policy.
func TestUnsupportedHistoryFailsClosed(t *testing.T) {
	for _, body := range []string{
		`{"input":[{"role":"user","content":[{"type":"input_image","file_id":"opaque-image"}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"application/pdf","data":"pdf"}}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"document"}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"input_audio"}]}]}`,
		`{"input":[{"role":"user","content":[{"type":"input_file"}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"future_media"}]}]}`,
		`{"messages":[{"role":"user","content":[{"unrecognized":"media"}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"tool_result","content":[{"type":"document"}]}]}]}`,
		`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"video/mp4","data":"video"}}]}]}`,
		`{"contents":[{"role":"user","parts":[{"fileData":{"fileUri":"unknown"}}]}]}`,
		`{"contents":[{"role":"user","parts":[{"functionResponse":{"parts":[{"inlineData":{"mimeType":"audio/wav"}}]}}]}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, reason := TruncationShape([]byte(body))
			require.NotEmpty(t, reason)
			counted := false
			_, _, err := Trim(context.Background(), []byte(body), 100, 1, func([]byte) (int, error) { counted = true; return 1, nil })
			require.ErrorContains(t, err, "context_truncation_unsupported_content")
			assert.False(t, counted)
		})
	}
}
