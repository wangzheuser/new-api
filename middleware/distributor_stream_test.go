package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetModelRequestPreservesStreamForTextProtocolRouting verifies that channel selection sees the client's stream mode.
func TestGetModelRequestPreservesStreamForTextProtocolRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/messages",
		strings.NewReader(`{"model":"glm-5.2","stream":true}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")

	request, shouldSelectChannel, err := getModelRequest(c)

	require.NoError(t, err)
	require.NotNil(t, request)
	assert.True(t, shouldSelectChannel)
	assert.Equal(t, "glm-5.2", request.Model)
	assert.True(t, request.Stream)
}
