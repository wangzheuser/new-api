package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestShouldRecordChannelAffinity verifies that response overrides do not
// change the successful upstream attempt used for affinity decisions.
func TestShouldRecordChannelAffinity(t *testing.T) {
	tests := []struct {
		name              string
		upstreamSucceeded bool
		clientStatus      int
		expected          bool
	}{
		{
			name:              "upstream success with client error",
			upstreamSucceeded: true,
			clientStatus:      http.StatusInternalServerError,
			expected:          true,
		},
		{
			name:         "ordinary client error",
			clientStatus: http.StatusInternalServerError,
			expected:     false,
		},
		{
			name:         "ordinary client success",
			clientStatus: http.StatusOK,
			expected:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expected, shouldRecordChannelAffinity(test.upstreamSucceeded, test.clientStatus))
		})
	}
}
