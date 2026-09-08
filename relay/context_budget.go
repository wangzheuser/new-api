package relay

import (
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
