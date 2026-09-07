package controller

import (
	"errors"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

// TestConversionErrorIsNotReportedAsUpstreamLoad preserves the local failure category.
func TestConversionErrorIsNotReportedAsUpstreamLoad(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		source := types.NewErrorWithStatusCode(errors.New("private request details"), types.ErrorCodeConvertRequestFailed, code, types.ErrOptionWithSkipRetry())
		result := resolveConfiguredFinalRelayError(nil, &relaycommon.RelayInfo{LastError: source})
		require.NotNil(t, result)
		assert.Equal(t, code, result.StatusCode)
		assert.Equal(t, types.ErrorCodeConvertRequestFailed, result.GetErrorCode())
		assert.True(t, types.IsSkipRetryError(result))
		assert.Contains(t, result.Error(), "protocol conversion")
		assert.NotContains(t, result.Error(), "private request details")
	}
}
