package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteContextFallbackResponseModels(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "json",
			input:    `{"id":"x","model":"MODEL_B"}`,
			expected: `{"id":"x","model":"MODEL_A"}`,
		},
		{
			name:     "responses event",
			input:    "data: {\"type\":\"response.created\",\"response\":{\"model\":\"MODEL_B\"}}\n\n",
			expected: "data: {\"type\":\"response.created\",\"response\":{\"model\":\"MODEL_A\"}}\n\n",
		},
		{
			name:     "done",
			input:    "data: [DONE]\n\n",
			expected: "data: [DONE]\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(rewriteContextFallbackResponseModels([]byte(tt.input), "MODEL_A")))
		})
	}
}

func TestContextFallbackResponseWriterRewritesSplitSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header("Content-Type", "text/event-stream")
	installContextFallbackResponseWriter(c, "MODEL_A")

	_, err := c.Writer.Write([]byte("data: {\"model\":\"MODE"))
	require.NoError(t, err)
	assert.Empty(t, recorder.Body.String())
	_, err = c.Writer.Write([]byte("L_B\"}\n\n"))
	require.NoError(t, err)

	assert.Equal(t, "data: {\"model\":\"MODEL_A\"}\n\n", recorder.Body.String())
}

func TestContextFallbackResponseWriterDropsStaleContentLength(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header("Content-Type", "application/json")
	c.Header("Content-Length", "24")
	installContextFallbackResponseWriter(c, "MODEL_A_LONG")

	c.Writer.WriteHeader(200)
	_, err := c.Writer.Write([]byte(`{"model":"MODEL_B"}`))

	require.NoError(t, err)
	assert.Empty(t, recorder.Header().Get("Content-Length"))
	assert.JSONEq(t, `{"model":"MODEL_A_LONG"}`, recorder.Body.String())
}
