package relay

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/contexttruncate"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// countContextBudget separates complete context checks from the existing billing estimate.
func countContextBudget(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request, body []byte) (billing, budget int, err error) {
	meta, err := contextImageTokenMeta(request)
	if err != nil {
		return 0, 0, err
	}
	billing, err = service.CountRequestToken(c, meta, info)
	if err != nil {
		return 0, 0, err
	}
	// The media-only pass must not overwrite the prompt count used by settlement fallbacks.
	defer common.SetContextKey(c, constant.ContextKeyPromptTokens, billing)
	meta.CombineText = ""
	media, err := service.CountRequestToken(c, meta, info)
	if err != nil {
		return billing, 0, err
	}
	budget, err = contexttruncate.Estimate(body, media)
	return billing, max(billing, budget), err
}

// decodeContextRequest replaces reused message storage so omitted fields cannot survive a trim.
func decodeContextRequest(request dto.Request, body []byte) error {
	switch request := request.(type) {
	case *dto.GeneralOpenAIRequest:
		return replaceContextRequest(request, body)
	case *dto.ClaudeRequest:
		return replaceContextRequest(request, body)
	case *dto.OpenAIResponsesRequest:
		return replaceContextRequest(request, body)
	case *dto.OpenAIResponsesCompactionRequest:
		return replaceContextRequest(request, body)
	case *dto.GeminiChatRequest:
		return replaceContextRequest(request, body)
	default:
		return fmt.Errorf("context_truncation_unsupported_conversion")
	}
}

// replaceContextRequest commits a freshly decoded DTO only after the full body is valid.
func replaceContextRequest[T any](request *T, body []byte) error {
	var fresh T
	if err := common.Unmarshal(body, &fresh); err != nil {
		return err
	}
	*request = fresh
	return nil
}
