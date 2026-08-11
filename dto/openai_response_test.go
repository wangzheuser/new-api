package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesStreamResponseGetOpenAIError(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantMessage string
		wantCode    interface{}
	}{
		{
			name:        "response failed nested error",
			payload:     `{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"stream_aborted","message":"mid stream aborted"}}}`,
			wantMessage: "mid stream aborted",
			wantCode:    "stream_aborted",
		},
		{
			name:        "top-level error event",
			payload:     `{"type":"error","code":"server_error","message":"provider unavailable","param":null}`,
			wantMessage: "provider unavailable",
			wantCode:    "server_error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response ResponsesStreamResponse
			require.NoError(t, common.UnmarshalJsonStr(test.payload, &response))

			openAIError := response.GetOpenAIError()

			require.NotNil(t, openAIError)
			assert.Equal(t, test.wantMessage, openAIError.Message)
			assert.Equal(t, test.wantCode, openAIError.Code)
		})
	}
}
