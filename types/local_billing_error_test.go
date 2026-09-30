package types

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLocalBillingErrorDoesNotCaptureUpstreamCodes 防止上游同名错误绕开健康策略。
func TestLocalBillingErrorDoesNotCaptureUpstreamCodes(t *testing.T) {
	for _, code := range []ErrorCode{ErrorCodeInsufficientUserQuota, ErrorCodePreConsumeTokenQuotaFailed, ErrorCodeUpdateDataError} {
		err := NewErrorWithStatusCode(errors.New("failure"), code, http.StatusForbidden)
		assert.True(t, IsLocalBillingError(err))
		upstream := NewErrorWithStatusCode(errors.New("failure"), code, http.StatusForbidden, ErrOptionWithUpstreamStatusCode(403))
		assert.False(t, IsLocalBillingError(upstream))
		provider := NewOpenAIError(errors.New("failure"), code, http.StatusForbidden)
		assert.False(t, IsLocalBillingError(provider))
	}
	assert.False(t, IsLocalBillingError(nil))
}
