package claude

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestResponsesRequestUsesRegisteredClaudeConversion protects the failing production route.
func TestResponsesRequestUsesRegisteredClaudeConversion(t *testing.T) {
	input, err := common.Marshal("hello")
	require.NoError(t, err)
	result, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{Model: "k3-256k", Input: input})
	require.NoError(t, err)
	request, ok := result.(*dto.ClaudeRequest)
	require.True(t, ok)
	assert.Equal(t, "k3-256k", request.Model)
	assert.NotEmpty(t, request.Messages)
}

// TestClaudeNonStreamClientFormats verifies that both bridge outputs contain actual text.
func TestClaudeNonStreamClientFormats(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAIResponses, types.RelayFormatGemini} {
		t.Run(string(format), func(t *testing.T) {
			ctx, recorder, info, response := newClaudeStreamFixture("")
			info.IsStream = false
			info.RelayFormat = format
			response.Header = http.Header{"Content-Type": []string{"application/json"}}
			response.Body = io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","model":"MODEL_X","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`))
			usage, err := ClaudeHandler(ctx, response, info)
			require.Nil(t, err)
			require.NotNil(t, usage)
			assert.Equal(t, 3, usage.TotalTokens)
			assert.Contains(t, recorder.Body.String(), "hello")
			var decoded map[string]any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &decoded))
			if format == types.RelayFormatOpenAIResponses {
				assert.NotEmpty(t, decoded["output"])
			} else {
				assert.NotEmpty(t, decoded["candidates"])
			}
		})
	}
}
