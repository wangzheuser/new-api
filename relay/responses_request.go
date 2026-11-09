package relay

import (
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

// PrepareResponsesRequest applies the same model, conversion and channel rules
// for HTTP and WebSocket requests. The caller closes closer after the attempt;
// passthrough bodies remain owned by the incoming request's BodyStorage.
// The returned adaptor retains route/conversion state for DoRequest/DoResponse.
func PrepareResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) (relaychannel.Adaptor, common.ReplayableBody, io.Closer, *types.NewAPIError) {
	adaptor, request, passThrough, apiErr := prepareResponsesAttempt(c, info, req)
	if apiErr != nil {
		return nil, nil, nil, apiErr
	}
	body, closer, apiErr := serializeResponsesRequest(c, info, adaptor, request, passThrough)
	return adaptor, body, closer, apiErr
}

// prepareResponsesAttempt keeps HTTP and WebSocket attempts on the same input policies.
func prepareResponsesAttempt(c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) (relaychannel.Adaptor, *dto.OpenAIResponsesRequest, bool, *types.NewAPIError) {
	info.InitRequestConversionChain()
	info.InitChannelMeta(c)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		!common.SupportsResponsesCompact(info.ChannelType, info.ApiType) {
		return nil, nil, false, types.NewErrorWithStatusCode(
			fmt.Errorf("unsupported endpoint %q for api type %d", "/v1/responses/compact", info.ApiType),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	request, err := common.DeepCopy(req)
	if err != nil {
		return nil, nil, false, types.NewError(fmt.Errorf("failed to copy responses request: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, nil, false, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ApplyReasoningModelSuffix(c, info, request); err != nil {
		return nil, nil, false, newConvertRequestFailedError(c, info, err)
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return nil, nil, false, types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)
	if promptErr := ApplySystemPromptForRequest(c, info, request); promptErr != nil {
		return nil, nil, false, promptErr
	}
	passThrough := (model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled) && textRouteAllowsPassThrough(info)
	if info.RelayMode != relayconstant.RelayModeResponsesCompact {
		if policyErr := prepareTextInputPolicies(c, info, request, passThrough); policyErr != nil {
			return nil, nil, false, policyErr
		}
	}
	return adaptor, request, passThrough, nil
}

// serializeResponsesRequest applies final-wire controls without preparing the attempt twice.
func serializeResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, adaptor relaychannel.Adaptor, request *dto.OpenAIResponsesRequest, passThrough bool) (common.ReplayableBody, io.Closer, *types.NewAPIError) {
	if passThrough {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, nil, types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		body := common.NewReplayableBodyReader(storage)
		return body, io.NopCloser(body), nil
	}

	convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
	if err != nil {
		return nil, nil, newConvertRequestFailedError(c, info, err)
	}
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
	jsonData, apiErr := PrepareTextRouteRequestBody(c, info, convertedRequest)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return nil, nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return body, closer, nil
}
