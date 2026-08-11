package helper

import (
	"fmt"
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// StreamStatusError converts an abnormal scanner end into a relay error.
func StreamStatusError(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	if info == nil || info.StreamStatus == nil || info.StreamStatus.IsNormalEnd() {
		return nil
	}
	reason, endErr := info.StreamStatus.End()
	if endErr == nil {
		endErr = fmt.Errorf("stream ended abnormally: %s", reason)
	} else {
		endErr = fmt.Errorf("stream ended abnormally (%s): %w", reason, endErr)
	}
	statusCode := http.StatusBadGateway
	if reason == relaycommon.StreamEndReasonTimeout {
		statusCode = http.StatusGatewayTimeout
	}
	options := make([]types.NewAPIErrorOptions, 0, 1)
	if c != nil && c.Writer != nil && c.Writer.Written() {
		options = append(options, types.ErrOptionWithSkipRetry())
	}
	return types.NewOpenAIError(endErr, types.ErrorCodeBadResponse, statusCode, options...)
}
