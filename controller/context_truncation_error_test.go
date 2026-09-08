package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// TestLocalTruncationErrorsStayClientVisible ensures local policy failures do not become generic gateway errors.
func TestLocalTruncationErrorsStayClientVisible(t *testing.T) {
	for _, code := range []string{"context_length_exceeded", "context_truncation_unsupported_content: multimodal", "context_truncation_final_budget_exceeded"} {
		t.Run(code, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			local := types.NewErrorWithStatusCode(errors.New(code), types.ErrorCode(code), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
			info := &relaycommon.RelayInfo{LastError: local, ContextTruncation: &relaycommon.ContextTruncationState{Enabled: true}}
			assert.Same(t, local, resolveConfiguredFinalRelayError(c, info))
			// The same text from an upstream response must still use the existing final-error rules.
			upstream := types.NewErrorWithStatusCode(errors.New(code), types.ErrorCode(code), http.StatusBadRequest, types.ErrOptionWithUpstreamStatusCode(http.StatusBadRequest))
			info.LastError = upstream
			withPolicy := resolveConfiguredFinalRelayError(c, info)
			info.ContextTruncation = nil
			withoutPolicy := resolveConfiguredFinalRelayError(c, info)
			assert.Equal(t, withoutPolicy.StatusCode, withPolicy.StatusCode)
			assert.Equal(t, withoutPolicy.GetErrorCode(), withPolicy.GetErrorCode())
			assert.Equal(t, withoutPolicy.Error(), withPolicy.Error())
		})
	}
}
