package types

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpstreamStatusCodeSurvivesClientStatusMapping(t *testing.T) {
	err := NewOpenAIError(
		errors.New("upstream unavailable"),
		ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
		ErrOptionWithUpstreamStatusCode(http.StatusServiceUnavailable),
	)
	err.StatusCode = http.StatusBadRequest

	statusCode, ok := err.GetUpstreamStatusCode()
	assert.True(t, ok)
	assert.Equal(t, http.StatusServiceUnavailable, statusCode)
	assert.Equal(t, http.StatusBadRequest, err.StatusCode)
}
