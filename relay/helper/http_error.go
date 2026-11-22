package helper

import (
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// WriteRelayHTTPError renders an error before the response stream has started.
func WriteRelayHTTPError(c *gin.Context, format types.RelayFormat, apiErr *types.NewAPIError, message string) {
	prepareRelayErrorHeaders(c)
	switch format {
	case types.RelayFormatClaude:
		clientError := apiErr.ToClaudeError()
		clientError.Message = message
		c.JSON(apiErr.StatusCode, gin.H{"type": "error", "error": clientError})
	case types.RelayFormatGemini:
		c.JSON(apiErr.StatusCode, dto.GeminiErrorResponse{
			Error: dto.GeminiError{
				Code: apiErr.StatusCode, Message: message, Status: geminiCanonicalErrorStatus(apiErr.StatusCode),
			},
		})
	default:
		clientError := apiErr.ToOpenAIError()
		clientError.Message = message
		c.JSON(apiErr.StatusCode, gin.H{"error": clientError})
	}
}

func geminiCanonicalErrorStatus(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound, http.StatusGone:
		return "NOT_FOUND"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	case http.StatusConflict:
		return "ABORTED"
	case http.StatusPreconditionFailed:
		return "FAILED_PRECONDITION"
	case http.StatusRequestEntityTooLarge, http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusRequestedRangeNotSatisfiable:
		return "OUT_OF_RANGE"
	case 499:
		return "CANCELLED"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	default:
		if statusCode >= http.StatusInternalServerError {
			return "INTERNAL"
		}
		return "UNKNOWN"
	}
}

func prepareRelayErrorHeaders(c *gin.Context) {
	if c == nil || c.Writer == nil {
		return
	}
	for _, key := range []string{"Content-Length", "Content-Encoding", "ETag", "Content-Range", "Transfer-Encoding"} {
		c.Writer.Header().Del(key)
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
}
