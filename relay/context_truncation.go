package relay

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/contexttruncate"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"net/http"
)

// prepareTextInputPolicies operates only on the handler's attempt-local request copy.
func prepareTextInputPolicies(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request, passThrough bool) *types.NewAPIError {
	rule, source, enabled := model_setting.ResolveContextTruncation(info.ChannelOtherSettings.ContextTruncation, info.GetAttemptModelName())
	if !enabled && info.CacheUsageSimulation == nil {
		return nil
	}
	body, err := common.Marshal(request)
	if err != nil {
		return inputPolicyError(err)
	}
	_, reason := contexttruncate.Shape(body)
	if s := info.CacheUsageSimulation; s != nil && reason != "" && reason != "provider_state_reference" {
		s.Reason = reason
	}
	if !enabled {
		return reserveInputPolicy(c, info, request, info.GetEstimatePromptTokens())
	}
	state := &relaycommon.ContextTruncationState{Rule: rule, Source: source, Model: info.GetAttemptModelName()}
	info.ContextTruncation = state
	if passThrough {
		state.Reason = "pass_through"
		return reserveInputPolicy(c, info, request, info.GetEstimatePromptTokens())
	}
	if reason != "" {
		state.Reason = reason
		return reserveInputPolicy(c, info, request, info.GetEstimatePromptTokens())
	}
	if info.ApiType != constant.APITypeOpenAI && info.ApiType != constant.APITypeAnthropic && info.ApiType != constant.APITypeGemini {
		state.Reason = "context_truncation_unsupported_conversion"
		return inputPolicyError(fmt.Errorf("%s", state.Reason))
	}
	if contexttruncate.Conflicts(info.ParamOverride) {
		state.Reason = "context_truncation_override_conflict"
		return inputPolicyError(fmt.Errorf("%s", state.Reason))
	}
	budget, err := rule.Budget(request.GetTokenCountMeta().MaxTokens)
	if err != nil {
		state.Reason = err.Error()
		return inputPolicyError(err)
	}
	state.Budget = budget
	// Reuse the concrete protocol DTO for counting; the source object is never changed here.
	count := func(b []byte) (int, error) {
		copy, err := copySystemPromptRequest(request)
		if err != nil {
			return 0, err
		}
		if err = common.Unmarshal(b, copy); err != nil {
			return 0, err
		}
		return service.CountRequestToken(c, copy.GetTokenCountMeta(), info)
	}
	trimmed, result, err := contexttruncate.Trim(c.Request.Context(), body, budget, rule.Keep(), count)
	state.Before = result.Before
	state.After = result.After
	state.Applied = result.Applied
	state.RemovedTurns = result.RemovedTurns
	state.RemovedMessages = result.RemovedMessages
	if err != nil {
		state.Reason = err.Error()
		return inputPolicyError(err)
	}
	if apiErr := reserveInputPolicy(c, info, request, result.Before); apiErr != nil {
		return apiErr
	}
	if result.Applied {
		if err := common.Unmarshal(trimmed, request); err != nil {
			return inputPolicyError(err)
		}
	}
	// Provider conversion may add prompt material or output defaults after the initial trim.
	info.ValidateInputPolicyBody = func(final []byte) error {
		var outgoing dto.Request
		switch info.GetFinalRequestRelayFormat() {
		case types.RelayFormatOpenAI:
			outgoing = &dto.GeneralOpenAIRequest{}
		case types.RelayFormatClaude:
			outgoing = &dto.ClaudeRequest{}
		case types.RelayFormatOpenAIResponses:
			outgoing = &dto.OpenAIResponsesRequest{}
		case types.RelayFormatGemini:
			outgoing = &dto.GeminiChatRequest{}
		default:
			return inputPolicyError(fmt.Errorf("context_truncation_unsupported_conversion"))
		}
		if _, reason := contexttruncate.Shape(final); reason != "" {
			return inputPolicyError(fmt.Errorf("context_truncation_unsupported_conversion"))
		}
		if err := common.Unmarshal(final, outgoing); err != nil {
			return inputPolicyError(err)
		}
		finalBudget, err := rule.Budget(outgoing.GetTokenCountMeta().MaxTokens)
		if err != nil {
			return inputPolicyError(err)
		}
		tokens, err := service.CountRequestToken(c, outgoing.GetTokenCountMeta(), info)
		if err != nil {
			return inputPolicyError(err)
		}
		if tokens > min(state.Budget, finalBudget) {
			state.Reason = "context_truncation_final_budget_exceeded"
			return inputPolicyError(fmt.Errorf("%s", state.Reason))
		}
		state.After = tokens
		return nil
	}

	return nil
}

// reserveInputPolicy extends the existing billing session, preserving its trust/free behavior.
func reserveInputPolicy(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request, tokens int) *types.NewAPIError {
	if info.Billing == nil || info.PriceData.FreeModel {
		return nil
	}
	if tokens <= 0 && info.CacheUsageSimulation != nil {
		var err error
		tokens, err = service.CountRequestToken(c, request.GetTokenCountMeta(), info)
		if err != nil {
			return inputPolicyError(err)
		}
	}
	amount, err := helper.EstimateQuotaWithFrozenPrice(info, tokens, request.GetTokenCountMeta())
	if err != nil {
		return inputPolicyError(err)
	}
	if info.CacheUsageSimulation != nil {
		extra, err := service.EstimateCacheSimulationReserve(c, info, tokens, request.GetTokenCountMeta().MaxTokens)
		if err != nil {
			return inputPolicyError(err)
		}
		amount = max(amount, extra)
	}
	if err = info.Billing.Reserve(amount); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// inputPolicyError returns a local pre-send configuration/budget error, not an upstream violation.
func inputPolicyError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCode(err.Error()), http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}
