package contexttruncate

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEstimateIncludesSentContext covers fields omitted by the billing DTO extractors.
func TestEstimateIncludesSentContext(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"assistant","tool_calls":[{"function":{"arguments":"%s"}}]}]}`,
		`{"messages":[{"role":"assistant","reasoning_content":"%s"}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"%s"}]}]}`,
		`{"input":[{"type":"function_call","arguments":"%s"}]}`,
		`{"contents":[{"parts":[{"functionCall":{"args":{"source":"%s"}}}]}]}`,
		`{"generationConfig":{"responseSchema":{"description":"%s"}}}`,
	} {
		small, err := Estimate([]byte(strings.Replace(body, "%s", "ok", 1)), 0)
		require.NoError(t, err)
		large, err := Estimate([]byte(strings.Replace(body, "%s", strings.Repeat("longFunctionSignature", 4000), 1)), 0)
		require.NoError(t, err)
		assert.Greater(t, large, small+4000)
	}
}

// TestEstimateMasksOnlyImageTransport protects binary image handling and arbitrary tool JSON.
func TestEstimateMasksOnlyImageTransport(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"%s"}}]}]}`,
		`{"messages":[{"content":[{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"%s"}}]}]}]}`,
		`{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"%s"}}]}]}`,
		`{"input":[{"type":"input_image","image_url":"%s"}]}`,
	} {
		small, err := Estimate([]byte(strings.Replace(body, "%s", "image", 1)), 520)
		require.NoError(t, err)
		large, err := Estimate([]byte(strings.Replace(body, "%s", strings.Repeat("binary", 10000), 1)), 520)
		require.NoError(t, err)
		assert.Equal(t, small, large)
		assert.GreaterOrEqual(t, small, 520)
	}
	// A tool input that resembles an image block is still ordinary input data.
	body, err := common.Marshal(map[string]any{"messages": []any{map[string]any{"content": []any{map[string]any{"type": "tool_use", "input": map[string]any{"type": "image", "source": strings.Repeat("dataValue", 5000)}}}}}})
	require.NoError(t, err)
	n, err := Estimate(body, 0)
	require.NoError(t, err)
	assert.Greater(t, n, 5000)
}
