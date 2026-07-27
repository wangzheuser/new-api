package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

// TestRelayClientErrorLogContent verifies that final logs use the structured client-facing error.
func TestRelayClientErrorLogContent(t *testing.T) {
	t.Run("OpenAI structured error", func(t *testing.T) {
		err := types.WithOpenAIError(types.OpenAIError{
			Message: "最终响应",
			Type:    "upstream_error",
			Code:    "upstream_error",
		}, http.StatusServiceUnavailable)
		err.SetMessage("internal wrapper (request id: req-test)")

		require.Equal(t, "status_code=503, 最终响应", relayClientErrorLogContent(err, types.RelayFormatOpenAI))
	})

	t.Run("Claude structured error", func(t *testing.T) {
		err := types.WithClaudeError(types.ClaudeError{
			Message: "最终响应",
			Type:    "upstream_error",
		}, http.StatusServiceUnavailable)
		err.SetMessage("internal wrapper (request id: req-test)")

		require.Equal(t, "status_code=503, 最终响应", relayClientErrorLogContent(err, types.RelayFormatClaude))
	})

	t.Run("local error includes request id", func(t *testing.T) {
		err := types.NewErrorWithStatusCode(errors.New("最终响应"), types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)
		err.SetMessage("最终响应 (request id: req-test)")

		require.Equal(
			t,
			"status_code=503, 最终响应 (request id: req-test)",
			relayClientErrorLogContent(err, types.RelayFormatOpenAI),
		)
	})
}
