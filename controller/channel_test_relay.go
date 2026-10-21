package controller

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// testTextChannel captures a real client-protocol relay against the selected channel snapshot.
func testTextChannel(c *gin.Context, recorder *httptest.ResponseRecorder, channel *model.Channel, request dto.Request, format types.RelayFormat, endpoint constant.EndpointType, options channelTestOptions) testResult {
	result := testResult{context: c, effectiveEndpointType: endpoint, isStream: options.isStream}
	body, err := common.Marshal(request)
	if err != nil {
		result.localErr = err
		result.newAPIError = types.NewError(err, types.ErrorCodeJsonMarshalFailed)
		return result
	}
	if path, ok := service.BuildTextProtocolPath(endpoint, common.GetContextKeyString(c, constant.ContextKeyOriginalModel), options.isStream); ok {
		c.Request.URL, err = url.Parse(path)
		if err != nil {
			result.localErr = err
			result.newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest)
			return result
		}
		c.Request.RequestURI = c.Request.URL.RequestURI()
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	c.Set("token_name", "模型测试")
	c.Set("specific_channel_id", channel.Id)
	defer common.CleanupBodyStorage(c)

	info, apiErr := executeRelayRequest(c, format, channel)
	if info != nil && info.ChannelMeta != nil {
		result.upstreamModel = info.UpstreamModelName
		switch info.GetFinalRequestRelayFormat() {
		case types.RelayFormatOpenAI:
			result.upstreamEndpointType = constant.EndpointTypeOpenAI
		case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
			result.upstreamEndpointType = constant.EndpointTypeOpenAIResponse
			if endpoint == constant.EndpointTypeOpenAIResponseCompact {
				result.upstreamEndpointType = endpoint
			}
		case types.RelayFormatClaude:
			result.upstreamEndpointType = constant.EndpointTypeAnthropic
		case types.RelayFormatGemini:
			result.upstreamEndpointType = constant.EndpointTypeGemini
		}
	}
	if common.GetContextKeyBool(c, constant.ContextKeyRelayUpstreamSucceeded) {
		result.upstreamStatus = http.StatusOK
	} else if info != nil && info.LastError != nil {
		result.upstreamStatus, _ = info.LastError.GetUpstreamStatusCode()
	}
	if buffer := relaycommon.CurrentResponseOverrideBuffer(c); buffer != nil {
		buffer.Discard(c)
	}
	if apiErr != nil {
		result.localErr = apiErr
		result.newAPIError = apiErr
		return result
	}
	response := recorder.Result()
	defer response.Body.Close()
	result.responseBody, result.responseBodyTruncated, err = readTestResponseBody(response.Body, options.isStream)
	if err == nil {
		err = validateTestResponseBody(result.responseBody, options.isStream)
	}
	if err != nil {
		result.localErr = err
		result.newAPIError = types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	return result
}
