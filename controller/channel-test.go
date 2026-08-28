package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/samber/lo"
	"github.com/tidwall/gjson"

	"github.com/gin-gonic/gin"
)

type testResult struct {
	context               *gin.Context
	localErr              error
	newAPIError           *types.NewAPIError
	responseBody          []byte
	responseBodyTruncated bool
	upstreamStatus        int
	effectiveEndpointType constant.EndpointType
	isStream              bool
}

type channelTestResponseDetails struct {
	EffectiveEndpointType constant.EndpointType `json:"effective_endpoint_type"`
	Stream                bool                  `json:"stream"`
	Content               string                `json:"content,omitempty"`
	ReasoningContent      string                `json:"reasoning_content,omitempty"`
	RawResponse           string                `json:"raw_response"`
	RawResponseTruncated  bool                  `json:"raw_response_truncated"`
}

type channelTestOptions struct {
	model                      string
	endpointType               string
	isStream                   bool
	userPrompt                 string
	maxOutputTokens            uint
	applySystemPrompt          bool
	requireSystemPromptSupport bool
	nativeProbe                bool
	protocolProbeCase          protocolProbeCase
	protocolProbeExecution     protocolProbeExecution
	keyIndex                   *int
}

type channelTestResponseOptions struct {
	includeDetails     bool
	keyIndex           *int
	updateResponseTime bool
}

type channelPromptTestRequest struct {
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
}

type channelConnectionTestRequest struct {
	Model        string `json:"model"`
	UserPrompt   string `json:"user_prompt"`
	EndpointType string `json:"endpoint_type"`
	Stream       bool   `json:"stream"`
}

const maxChannelPromptTestUserPromptBytes = 16 * 1024
const customChannelTestMaxOutputTokens = 1024
const maxChannelTestResponseDetailBytes = 64 * 1024

// getChannelConnectionTestMaxOutputTokens returns the expanded output limit for a custom prompt.
func getChannelConnectionTestMaxOutputTokens(userPrompt string) uint {
	if userPrompt == "hi" {
		return 0
	}
	return customChannelTestMaxOutputTokens
}

// normalizeChannelTestEndpoint preserves explicit endpoint selection and the existing automatic endpoint priority.
func normalizeChannelTestEndpoint(channel *model.Channel, modelName, endpointType string) string {
	normalized := strings.TrimSpace(endpointType)
	if normalized != "" {
		return normalized
	}
	if strings.HasSuffix(modelName, ratio_setting.CompactModelSuffix) {
		return string(constant.EndpointTypeOpenAIResponseCompact)
	}
	if channel != nil && channel.Type == constant.ChannelTypeCodex {
		return string(constant.EndpointTypeOpenAIResponse)
	}
	return normalized
}

// resolveChannelTestRequestPath applies the existing endpoint-selection priority to one test model.
func resolveChannelTestRequestPath(channel *model.Channel, modelName, endpointType string) string {
	if endpointType != "" {
		if endpointInfo, ok := common.GetDefaultEndpointInfo(constant.EndpointType(endpointType)); ok {
			return endpointInfo.Path
		}
	}

	requestPath := "/v1/chat/completions"
	lowerModelName := strings.ToLower(modelName)
	if strings.Contains(lowerModelName, "rerank") {
		requestPath = "/v1/rerank"
	}
	if strings.Contains(lowerModelName, "embedding") ||
		strings.HasPrefix(modelName, "m3e") ||
		strings.Contains(modelName, "bge-") ||
		strings.Contains(modelName, "embed") ||
		channel != nil && channel.Type == constant.ChannelTypeMokaAI {
		requestPath = "/v1/embeddings"
	}
	if channel != nil && channel.Type == constant.ChannelTypeVolcEngine && strings.Contains(modelName, "seedream") {
		requestPath = "/v1/images/generations"
	}
	if strings.Contains(lowerModelName, "codex") {
		requestPath = "/v1/responses"
	}
	if strings.HasSuffix(modelName, ratio_setting.CompactModelSuffix) {
		requestPath = "/v1/responses/compact"
	}
	return requestPath
}

// resolveChannelTestEndpointType returns the client-visible endpoint represented by the final test request path.
func resolveChannelTestEndpointType(requestPath string) constant.EndpointType {
	path := strings.SplitN(requestPath, "?", 2)[0]
	switch {
	case strings.HasPrefix(path, "/v1/responses/compact"):
		return constant.EndpointTypeOpenAIResponseCompact
	case path == "/v1/responses":
		return constant.EndpointTypeOpenAIResponse
	case path == "/v1/messages":
		return constant.EndpointTypeAnthropic
	case (strings.Contains(path, "/v1beta/models/") || strings.Contains(path, "/v1/models/")) &&
		(strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent")):
		return constant.EndpointTypeGemini
	case path == "/v1/rerank" || path == "/rerank":
		return constant.EndpointTypeJinaRerank
	case path == "/v1/images/generations":
		return constant.EndpointTypeImageGeneration
	case path == "/v1/embeddings":
		return constant.EndpointTypeEmbeddings
	default:
		return constant.EndpointTypeOpenAI
	}
}

// channelTestEndpointSupportsStream reports whether the selected client protocol has a streaming test contract.
func channelTestEndpointSupportsStream(endpointType constant.EndpointType) bool {
	switch endpointType {
	case constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini:
		return true
	default:
		return false
	}
}

func resolveChannelTestUserID(c *gin.Context) (int, error) {
	if c != nil {
		if userID := c.GetInt("id"); userID > 0 {
			return userID, nil
		}
	}

	var rootUser model.User
	if err := model.DB.Select("id").Where("role = ?", common.RoleRootUser).First(&rootUser).Error; err != nil {
		return 0, fmt.Errorf("failed to resolve channel test user: %w", err)
	}
	if rootUser.Id == 0 {
		return 0, errors.New("failed to resolve channel test user")
	}
	return rootUser.Id, nil
}

func testChannel(ctx context.Context, channel *model.Channel, testUserID int, options channelTestOptions) testResult {
	if ctx == nil {
		ctx = context.Background()
	}
	tik := time.Now()
	var unsupportedTestChannelTypes = []int{
		constant.ChannelTypeMidjourney,
		constant.ChannelTypeMidjourneyPlus,
		constant.ChannelTypeSunoAPI,
		constant.ChannelTypeKling,
		constant.ChannelTypeJimeng,
		constant.ChannelTypeDoubaoVideo,
		constant.ChannelTypeVidu,
	}
	if lo.Contains(unsupportedTestChannelTypes, channel.Type) {
		channelTypeName := constant.GetChannelTypeName(channel.Type)
		return testResult{
			localErr: fmt.Errorf("%s channel test is not supported", channelTypeName),
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	testModel := strings.TrimSpace(options.model)
	if testModel == "" {
		if channel.TestModel != nil && *channel.TestModel != "" {
			testModel = strings.TrimSpace(*channel.TestModel)
		} else {
			models := channel.GetModels()
			if len(models) > 0 {
				testModel = strings.TrimSpace(models[0])
			}
			if testModel == "" {
				testModel = "gpt-4o-mini"
			}
		}
	}

	endpointType := normalizeChannelTestEndpoint(channel, testModel, options.endpointType)
	directNativeProbe := options.nativeProbe && channel.Type != constant.ChannelTypeAdvancedCustom

	requestPath := resolveChannelTestRequestPath(channel, testModel, endpointType)
	if strings.HasPrefix(requestPath, "/v1/responses/compact") {
		testModel = ratio_setting.WithCompactModelSuffix(testModel)
	}
	if directNativeProbe {
		if nativePath, ok := service.BuildTextProtocolPath(constant.EndpointType(endpointType), testModel, options.isStream); ok {
			requestPath = nativePath
		}
	}
	effectiveEndpointType := resolveChannelTestEndpointType(requestPath)
	if options.isStream && !channelTestEndpointSupportsStream(effectiveEndpointType) {
		err := fmt.Errorf("%s endpoint only accepts non-streaming channel tests", effectiveEndpointType)
		return testResult{
			localErr:              err,
			newAPIError:           types.NewError(err, types.ErrorCodeInvalidRequest),
			effectiveEndpointType: effectiveEndpointType,
		}
	}

	c.Request = httptest.NewRequestWithContext(ctx, http.MethodPost, requestPath, nil)

	cache, err := model.GetUserCache(testUserID)
	if err != nil {
		return testResult{
			localErr:    err,
			newAPIError: nil,
		}
	}
	cache.WriteContext(c)
	c.Set("id", testUserID)

	//c.Request.Header.Set("Authorization", "Bearer "+channel.Key)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("channel", channel.Type)
	c.Set("base_url", channel.GetBaseURL())
	group, _ := model.GetUserGroup(testUserID, false)
	c.Set("group", group)
	common.SetContextKey(c, constant.ContextKeyIsStream, options.isStream)

	setupChannel := channel
	if directNativeProbe {
		channelCopy := *channel
		settings := channelCopy.GetSetting()
		settings.ProtocolPolicy = nil
		channelCopy.SetSetting(settings)
		setupChannel = &channelCopy
	}
	var newAPIError *types.NewAPIError
	if options.keyIndex == nil {
		newAPIError = middleware.SetupContextForSelectedChannel(c, setupChannel, testModel)
	} else {
		newAPIError = middleware.SetupContextForSelectedChannelKey(c, setupChannel, testModel, *options.keyIndex)
	}
	if newAPIError != nil {
		return testResult{
			context:     c,
			localErr:    newAPIError,
			newAPIError: newAPIError,
		}
	}

	// Determine relay format based on endpoint type or request path
	var relayFormat types.RelayFormat
	if endpointType != "" {
		// 根据指定的端点类型设置 relayFormat
		switch constant.EndpointType(endpointType) {
		case constant.EndpointTypeOpenAI:
			relayFormat = types.RelayFormatOpenAI
		case constant.EndpointTypeOpenAIResponse:
			relayFormat = types.RelayFormatOpenAIResponses
		case constant.EndpointTypeOpenAIResponseCompact:
			relayFormat = types.RelayFormatOpenAIResponsesCompaction
		case constant.EndpointTypeAnthropic:
			relayFormat = types.RelayFormatClaude
		case constant.EndpointTypeGemini:
			relayFormat = types.RelayFormatGemini
		case constant.EndpointTypeJinaRerank:
			relayFormat = types.RelayFormatRerank
		case constant.EndpointTypeImageGeneration:
			relayFormat = types.RelayFormatOpenAIImage
		case constant.EndpointTypeEmbeddings:
			relayFormat = types.RelayFormatEmbedding
		default:
			relayFormat = types.RelayFormatOpenAI
		}
	} else {
		// 根据请求路径自动检测
		relayFormat = types.RelayFormatOpenAI
		if c.Request.URL.Path == "/v1/embeddings" {
			relayFormat = types.RelayFormatEmbedding
		}
		if c.Request.URL.Path == "/v1/images/generations" {
			relayFormat = types.RelayFormatOpenAIImage
		}
		if c.Request.URL.Path == "/v1/messages" {
			relayFormat = types.RelayFormatClaude
		}
		if strings.Contains(c.Request.URL.Path, "/v1beta/models") {
			relayFormat = types.RelayFormatGemini
		}
		if c.Request.URL.Path == "/v1/rerank" || c.Request.URL.Path == "/rerank" {
			relayFormat = types.RelayFormatRerank
		}
		if c.Request.URL.Path == "/v1/responses" {
			relayFormat = types.RelayFormatOpenAIResponses
		}
		if strings.HasPrefix(c.Request.URL.Path, "/v1/responses/compact") {
			relayFormat = types.RelayFormatOpenAIResponsesCompaction
		}
	}

	requestEndpointType := endpointType
	if requestEndpointType == "" {
		requestEndpointType = string(effectiveEndpointType)
	}
	request := buildTestRequest(testModel, requestEndpointType, channel, options)
	if directNativeProbe {
		request, err = buildProtocolProbeRequest(testModel, constant.EndpointType(endpointType), options)
		if err != nil {
			return testResult{
				context:     c,
				localErr:    err,
				newAPIError: types.NewError(err, types.ErrorCodeInvalidRequest),
			}
		}
	}
	if options.requireSystemPromptSupport {
		switch request.(type) {
		case *dto.GeneralOpenAIRequest,
			*dto.OpenAIResponsesRequest,
			*dto.OpenAIResponsesCompactionRequest,
			*dto.ClaudeRequest,
			*dto.GeminiChatRequest:
		default:
			return testResult{
				context:     c,
				localErr:    errors.New("system prompt effect test only supports text generation models"),
				newAPIError: types.NewError(errors.New("unsupported model endpoint"), types.ErrorCodeInvalidRequest),
			}
		}
	}

	info, err := relaycommon.GenRelayInfo(c, relayFormat, request, nil)

	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeGenRelayInfoFailed),
		}
	}
	if info.IsStream != options.isStream {
		err = fmt.Errorf(
			"channel test stream mode mismatch for %s: requested=%t actual=%t",
			effectiveEndpointType,
			options.isStream,
			info.IsStream,
		)
		return testResult{
			context:               c,
			localErr:              err,
			newAPIError:           types.NewError(err, types.ErrorCodeGenRelayInfoFailed),
			effectiveEndpointType: effectiveEndpointType,
			isStream:              info.IsStream,
		}
	}
	if directNativeProbe {
		info.ChannelRoutePlan = protocolProbeRoutePlan(
			constant.EndpointType(endpointType),
			relayFormat,
			requestPath,
			info.RelayMode,
			options,
		)
	}

	info.IsChannelTest = true
	info.InitChannelMeta(c)
	defer func() {
		// Early failures must release the attempt-scoped writer chain before the test context is discarded.
		if buffer := relaycommon.CurrentResponseOverrideBuffer(c); buffer != nil {
			buffer.MarkRelayError()
			buffer.Discard(c)
		}
	}()

	err = attachTestBillingRequestInput(info, request)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeJsonMarshalFailed),
		}
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeChannelModelMappedError),
		}
	}

	testModel = info.UpstreamModelName
	// 更新请求中的模型名称
	request.SetModelName(testModel)
	if directNativeProbe {
		if nativePath, ok := service.BuildTextProtocolPath(constant.EndpointType(endpointType), testModel, info.IsStream); ok {
			c.Request.URL.Path = strings.SplitN(nativePath, "?", 2)[0]
			c.Request.URL.RawQuery = ""
			if pathParts := strings.SplitN(nativePath, "?", 2); len(pathParts) == 2 {
				c.Request.URL.RawQuery = pathParts[1]
			}
			info.RequestURLPath = nativePath
			info.RelayMode = relayconstant.Path2RelayMode(c.Request.URL.Path)
			if info.ChannelRoutePlan != nil {
				info.ChannelRoutePlan.UpstreamPath = nativePath
				info.ChannelRoutePlan.UpstreamRelayMode = info.RelayMode
			}
		}
		info.FinalRequestRelayFormat = relayFormat
	}
	if options.applySystemPrompt {
		if apiErr := relay.ApplySystemPromptForRequest(c, info, request); apiErr != nil {
			return testResult{
				context:     c,
				localErr:    apiErr,
				newAPIError: apiErr,
			}
		}
	}

	apiType, _ := common.ChannelType2APIType(channel.Type)
	if info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		apiType != constant.APITypeOpenAI &&
		apiType != constant.APITypeCodex {
		return testResult{
			context:     c,
			localErr:    fmt.Errorf("responses compaction test only supports openai/codex channels, got api type %d", apiType),
			newAPIError: types.NewError(fmt.Errorf("unsupported api type: %d", apiType), types.ErrorCodeInvalidApiType),
		}
	}
	adaptor := relay.GetAdaptor(apiType)
	if adaptor == nil {
		return testResult{
			context:     c,
			localErr:    fmt.Errorf("invalid api type: %d, adaptor is nil", apiType),
			newAPIError: types.NewError(fmt.Errorf("invalid api type: %d, adaptor is nil", apiType), types.ErrorCodeInvalidApiType),
		}
	}

	//// 创建一个用于日志的 info 副本，移除 ApiKey
	//logInfo := info
	//logInfo.ApiKey = ""
	common.SysLog(fmt.Sprintf("testing channel %d with model %s , info %+v ", channel.Id, testModel, info.ToString()))

	priceData, err := helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeModelPriceError, types.ErrOptionWithStatusCode(http.StatusBadRequest)),
		}
	}

	adaptor.Init(info)

	var convertedRequest any
	if directNativeProbe {
		convertedRequest = request
	} else {
		// 根据 RelayMode 选择正确的转换函数
		switch info.RelayMode {
		case relayconstant.RelayModeEmbeddings:
			// Embedding 请求 - request 已经是正确的类型
			if embeddingReq, ok := request.(*dto.EmbeddingRequest); ok {
				convertedRequest, err = adaptor.ConvertEmbeddingRequest(c, info, *embeddingReq)
			} else {
				return testResult{
					context:     c,
					localErr:    errors.New("invalid embedding request type"),
					newAPIError: types.NewError(errors.New("invalid embedding request type"), types.ErrorCodeConvertRequestFailed),
				}
			}
		case relayconstant.RelayModeImagesGenerations:
			// 图像生成请求 - request 已经是正确的类型
			if imageReq, ok := request.(*dto.ImageRequest); ok {
				convertedRequest, err = adaptor.ConvertImageRequest(c, info, *imageReq)
			} else {
				return testResult{
					context:     c,
					localErr:    errors.New("invalid image request type"),
					newAPIError: types.NewError(errors.New("invalid image request type"), types.ErrorCodeConvertRequestFailed),
				}
			}
		case relayconstant.RelayModeRerank:
			// Rerank 请求 - request 已经是正确的类型
			if rerankReq, ok := request.(*dto.RerankRequest); ok {
				convertedRequest, err = adaptor.ConvertRerankRequest(c, info.RelayMode, *rerankReq)
			} else {
				return testResult{
					context:     c,
					localErr:    errors.New("invalid rerank request type"),
					newAPIError: types.NewError(errors.New("invalid rerank request type"), types.ErrorCodeConvertRequestFailed),
				}
			}
		case relayconstant.RelayModeResponses:
			// Response 请求 - request 已经是正确的类型
			if responseReq, ok := request.(*dto.OpenAIResponsesRequest); ok {
				convertedRequest, err = adaptor.ConvertOpenAIResponsesRequest(c, info, *responseReq)
			} else {
				return testResult{
					context:     c,
					localErr:    errors.New("invalid response request type"),
					newAPIError: types.NewError(errors.New("invalid response request type"), types.ErrorCodeConvertRequestFailed),
				}
			}
		case relayconstant.RelayModeResponsesCompact:
			// Response compaction request - convert to OpenAIResponsesRequest before adapting
			switch req := request.(type) {
			case *dto.OpenAIResponsesCompactionRequest:
				convertedRequest, err = adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
					Model:              req.Model,
					Input:              req.Input,
					Instructions:       req.Instructions,
					PreviousResponseID: req.PreviousResponseID,
				})
			case *dto.OpenAIResponsesRequest:
				convertedRequest, err = adaptor.ConvertOpenAIResponsesRequest(c, info, *req)
			default:
				return testResult{
					context:     c,
					localErr:    errors.New("invalid response compaction request type"),
					newAPIError: types.NewError(errors.New("invalid response compaction request type"), types.ErrorCodeConvertRequestFailed),
				}
			}
		default:
			// Chat/Completion 等其他请求类型
			if generalReq, ok := request.(*dto.GeneralOpenAIRequest); ok {
				convertedRequest, err = adaptor.ConvertOpenAIRequest(c, info, generalReq)
			} else {
				return testResult{
					context:     c,
					localErr:    errors.New("invalid general request type"),
					newAPIError: types.NewError(errors.New("invalid general request type"), types.ErrorCodeConvertRequestFailed),
				}
			}
		}
	}

	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewError(err, types.ErrorCodeConvertRequestFailed),
		}
	}
	var jsonData []byte
	if directNativeProbe && options.protocolProbeCase.IsSemantic() {
		var apiErr *types.NewAPIError
		jsonData, apiErr = relay.PrepareTextRouteRequestBody(c, info, convertedRequest)
		if apiErr != nil {
			return testResult{context: c, localErr: apiErr, newAPIError: apiErr}
		}
	} else {
		jsonData, err = common.Marshal(convertedRequest)
		if err != nil {
			return testResult{
				context:     c,
				localErr:    err,
				newAPIError: types.NewError(err, types.ErrorCodeJsonMarshalFailed),
			}
		}

		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				if fixedErr, ok := relaycommon.AsParamOverrideReturnError(err); ok {
					return testResult{
						context:     c,
						localErr:    fixedErr,
						newAPIError: relaycommon.NewAPIErrorFromParamOverride(fixedErr),
					}
				}
				return testResult{
					context:     c,
					localErr:    err,
					newAPIError: types.NewError(err, types.ErrorCodeChannelParamOverrideInvalid),
				}
			}
		}
	}

	requestBody := bytes.NewBuffer(jsonData)
	c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return testResult{
			context:     c,
			localErr:    err,
			newAPIError: types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError),
		}
	}
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			err := service.RelayErrorHandler(c.Request.Context(), httpResp, true)
			common.SysError(fmt.Sprintf(
				"channel test bad response: channel_id=%d name=%s type=%d model=%s endpoint_type=%s status=%d err=%v",
				channel.Id,
				channel.Name,
				channel.Type,
				testModel,
				endpointType,
				httpResp.StatusCode,
				err,
			))
			return testResult{
				context:        c,
				localErr:       err,
				newAPIError:    types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError),
				upstreamStatus: httpResp.StatusCode,
			}
		}
	}
	var usageA any
	var respErr *types.NewAPIError
	if directNativeProbe {
		usageA, respErr = relay.HandleNativeTextResponse(c, info, httpResp, relayFormat, info.IsStream)
	} else {
		usageA, respErr = adaptor.DoResponse(c, httpResp, info)
	}
	if respErr != nil {
		return testResult{
			context:        c,
			localErr:       respErr,
			newAPIError:    respErr,
			upstreamStatus: http.StatusOK,
		}
	}
	usage, usageErr := coerceTestUsage(usageA, info.IsStream, info.GetEstimatePromptTokens())
	if usageErr != nil {
		return testResult{
			context:        c,
			localErr:       usageErr,
			newAPIError:    types.NewOpenAIError(usageErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError),
			upstreamStatus: http.StatusOK,
		}
	}
	info.SetEstimatePromptTokens(usage.PromptTokens)
	common.SetContextKey(c, constant.ContextKeyRelayUpstreamSucceeded, true)
	upstreamStatus := http.StatusOK
	if httpResp != nil {
		upstreamStatus = httpResp.StatusCode
	}
	service.EvaluateResponseOverrideBeforeSettlement(c, info, usage, upstreamStatus)
	clientResponseError := finalizeResponseOverride(c, info)

	var respBody []byte
	var responseBodyTruncated bool
	if clientResponseError == nil {
		result := w.Result()
		respBody, responseBodyTruncated, err = readTestResponseBody(result.Body, info.IsStream)
		if err != nil {
			return testResult{
				context:               c,
				localErr:              err,
				newAPIError:           types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError),
				upstreamStatus:        upstreamStatus,
				effectiveEndpointType: effectiveEndpointType,
				isStream:              info.IsStream,
			}
		}
		if bodyErr := validateTestResponseBody(respBody, info.IsStream); bodyErr != nil {
			return testResult{
				context:               c,
				localErr:              bodyErr,
				newAPIError:           types.NewOpenAIError(bodyErr, types.ErrorCodeBadResponseBody, http.StatusInternalServerError),
				upstreamStatus:        upstreamStatus,
				effectiveEndpointType: effectiveEndpointType,
				isStream:              info.IsStream,
			}
		}
	}

	quota, tieredResult := settleTestQuota(info, priceData, usage)
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	consumedTime := float64(milliseconds) / 1000.0
	other := buildTestLogOther(c, info, priceData, usage, tieredResult)
	model.RecordConsumeLog(c, testUserID, model.RecordConsumeLogParams{
		ChannelId:        channel.Id,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		ModelName:        info.OriginModelName,
		TokenName:        "模型测试",
		Quota:            quota,
		Content:          "模型测试",
		UseTimeSeconds:   int(consumedTime),
		IsStream:         info.IsStream,
		Group:            info.UsingGroup,
		Other:            other,
	})
	common.SysLog(fmt.Sprintf("testing channel #%d completed, response_bytes=%d", channel.Id, len(respBody)))
	if clientResponseError != nil {
		return testResult{
			context:               c,
			localErr:              clientResponseError,
			newAPIError:           clientResponseError,
			upstreamStatus:        upstreamStatus,
			effectiveEndpointType: effectiveEndpointType,
			isStream:              info.IsStream,
		}
	}
	return testResult{
		context:               c,
		localErr:              nil,
		newAPIError:           nil,
		responseBody:          respBody,
		responseBodyTruncated: responseBodyTruncated,
		upstreamStatus:        upstreamStatus,
		effectiveEndpointType: effectiveEndpointType,
		isStream:              info.IsStream,
	}
}

func attachTestBillingRequestInput(info *relaycommon.RelayInfo, request dto.Request) error {
	if info == nil {
		return nil
	}

	input, err := helper.BuildBillingExprRequestInputFromRequest(request, info.RequestHeaders)
	if err != nil {
		return err
	}
	info.BillingRequestInput = &input
	return nil
}

func settleTestQuota(info *relaycommon.RelayInfo, priceData types.PriceData, usage *dto.Usage) (int, *billingexpr.TieredResult) {
	if usage != nil && info != nil && info.TieredBillingSnapshot != nil {
		isClaudeUsageSemantic := usage.UsageSemantic == "anthropic" || info.GetFinalRequestRelayFormat() == types.RelayFormatClaude
		usedVars := billingexpr.UsedVars(info.TieredBillingSnapshot.ExprString)
		if ok, quota, result := service.TryTieredSettle(info, service.BuildTieredTokenParams(usage, isClaudeUsageSemantic, usedVars)); ok {
			return quota, result
		}
	}

	quota := 0
	if !priceData.UsePrice {
		quota = usage.PromptTokens + int(math.Round(float64(usage.CompletionTokens)*priceData.CompletionRatio))
		quota = int(math.Round(float64(quota) * priceData.ModelRatio))
		if priceData.ModelRatio != 0 && quota <= 0 {
			quota = 1
		}
		return quota, nil
	}

	return int(priceData.ModelPrice * common.QuotaPerUnit), nil
}

func buildTestLogOther(c *gin.Context, info *relaycommon.RelayInfo, priceData types.PriceData, usage *dto.Usage, tieredResult *billingexpr.TieredResult) map[string]interface{} {
	other := service.GenerateTextOtherInfo(c, info, priceData.ModelRatio, priceData.GroupRatioInfo.GroupRatio, priceData.CompletionRatio,
		usage.PromptTokensDetails.CachedTokens, priceData.CacheRatio, priceData.ModelPrice, priceData.GroupRatioInfo.GroupSpecialRatio)
	if tieredResult != nil {
		service.InjectTieredBillingInfo(other, info, tieredResult)
	}
	return other
}

func coerceTestUsage(usageAny any, isStream bool, estimatePromptTokens int) (*dto.Usage, error) {
	switch u := usageAny.(type) {
	case *dto.Usage:
		return u, nil
	case dto.Usage:
		return &u, nil
	case nil:
		if !isStream {
			return nil, errors.New("usage is nil")
		}
		usage := &dto.Usage{
			PromptTokens: estimatePromptTokens,
		}
		usage.TotalTokens = usage.PromptTokens
		return usage, nil
	default:
		if !isStream {
			return nil, fmt.Errorf("invalid usage type: %T", usageAny)
		}
		usage := &dto.Usage{
			PromptTokens: estimatePromptTokens,
		}
		usage.TotalTokens = usage.PromptTokens
		return usage, nil
	}
}

func readTestResponseBody(body io.ReadCloser, isStream bool) ([]byte, bool, error) {
	defer func() { _ = body.Close() }()
	if !isStream {
		responseBody, err := io.ReadAll(body)
		return responseBody, false, err
	}

	responseBody, err := io.ReadAll(io.LimitReader(body, maxChannelTestResponseDetailBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(responseBody) <= maxChannelTestResponseDetailBytes {
		return responseBody, false, nil
	}
	truncatedBody, _ := truncateChannelTestResponseDetail(responseBody)
	return truncatedBody, true, nil
}

func detectErrorFromTestResponseBody(respBody []byte) error {
	b := bytes.TrimSpace(respBody)
	if len(b) == 0 {
		return nil
	}
	if message := detectErrorMessageFromJSONBytes(b); message != "" {
		return fmt.Errorf("upstream error: %s", message)
	}

	for _, line := range bytes.Split(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		if message := detectErrorMessageFromJSONBytes(payload); message != "" {
			return fmt.Errorf("upstream error: %s", message)
		}
	}

	return nil
}

func validateStreamTestResponseBody(respBody []byte) error {
	b := bytes.TrimSpace(respBody)
	if len(b) == 0 {
		return errors.New("stream response body is empty")
	}

	for _, line := range bytes.Split(b, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}

		return nil
	}

	return errors.New("stream response body does not contain a valid stream event")
}

func validateTestResponseBody(respBody []byte, isStream bool) error {
	if len(bytes.TrimSpace(respBody)) == 0 {
		if isStream {
			return errors.New("stream response body is empty")
		}
		return errors.New("response body is empty")
	}
	if bodyErr := detectErrorFromTestResponseBody(respBody); bodyErr != nil {
		return bodyErr
	}
	if isStream {
		return validateStreamTestResponseBody(respBody)
	}
	return nil
}

func shouldUseStreamForAutomaticChannelTest(channel *model.Channel) bool {
	return channel != nil && channel.Type == constant.ChannelTypeCodex
}

func detectErrorMessageFromJSONBytes(jsonBytes []byte) string {
	if len(jsonBytes) == 0 {
		return ""
	}
	if jsonBytes[0] != '{' && jsonBytes[0] != '[' {
		return ""
	}
	errVal := gjson.GetBytes(jsonBytes, "error")
	if !errVal.Exists() || errVal.Type == gjson.Null {
		return ""
	}

	message := gjson.GetBytes(jsonBytes, "error.message").String()
	if message == "" {
		message = gjson.GetBytes(jsonBytes, "error.error.message").String()
	}
	if message == "" && errVal.Type == gjson.String {
		message = errVal.String()
	}
	if message == "" {
		message = errVal.Raw
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "upstream returned error payload"
	}
	return message
}

func buildTestRequest(model string, endpointType string, channel *model.Channel, options channelTestOptions) dto.Request {
	userPrompt := options.userPrompt
	if userPrompt == "" {
		userPrompt = "hi"
	}
	testResponsesInput := json.RawMessage(common.GetJsonString([]map[string]string{{
		"role": "user", "content": userPrompt,
	}}))
	defaultResponsesInput := json.RawMessage(common.GetJsonString([]map[string]string{{
		"role": "user", "content": "hi",
	}}))

	// 根据端点类型构建不同的测试请求
	if endpointType != "" {
		switch constant.EndpointType(endpointType) {
		case constant.EndpointTypeEmbeddings:
			// 返回 EmbeddingRequest
			return &dto.EmbeddingRequest{
				Model: model,
				Input: []any{"hello world"},
			}
		case constant.EndpointTypeImageGeneration:
			// 返回 ImageRequest
			return &dto.ImageRequest{
				Model:  model,
				Prompt: "a cute cat",
				N:      lo.ToPtr(uint(1)),
				Size:   "1024x1024",
			}
		case constant.EndpointTypeJinaRerank:
			// 返回 RerankRequest
			return &dto.RerankRequest{
				Model:     model,
				Query:     "What is Deep Learning?",
				Documents: []any{"Deep Learning is a subset of machine learning.", "Machine learning is a field of artificial intelligence."},
				TopN:      lo.ToPtr(2),
			}
		case constant.EndpointTypeOpenAIResponse:
			// 返回 OpenAIResponsesRequest
			return &dto.OpenAIResponsesRequest{
				Model:           model,
				Input:           testResponsesInput,
				Stream:          lo.ToPtr(options.isStream),
				MaxOutputTokens: lo.Ternary(options.maxOutputTokens > 0, lo.ToPtr(options.maxOutputTokens), nil),
			}
		case constant.EndpointTypeOpenAIResponseCompact:
			// 返回 OpenAIResponsesCompactionRequest
			return &dto.OpenAIResponsesCompactionRequest{
				Model: model,
				Input: defaultResponsesInput,
			}
		case constant.EndpointTypeAnthropic, constant.EndpointTypeGemini, constant.EndpointTypeOpenAI:
			// 返回 GeneralOpenAIRequest
			maxTokens := uint(16)
			if constant.EndpointType(endpointType) == constant.EndpointTypeGemini {
				maxTokens = 3000
			}
			req := &dto.GeneralOpenAIRequest{
				Model:  model,
				Stream: lo.ToPtr(options.isStream),
				Messages: []dto.Message{
					{
						Role:    "user",
						Content: userPrompt,
					},
				},
				MaxTokens: lo.ToPtr(maxTokens),
			}
			if options.maxOutputTokens > 0 {
				if constant.EndpointType(endpointType) == constant.EndpointTypeOpenAI && dto.IsOpenAIReasoningOModel(model) {
					req.MaxTokens = nil
					req.MaxCompletionTokens = lo.ToPtr(options.maxOutputTokens)
				} else {
					req.MaxTokens = lo.ToPtr(options.maxOutputTokens)
				}
			}
			if options.isStream {
				req.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
			}
			return req
		}
	}

	// 自动检测逻辑（保持原有行为）
	if strings.Contains(strings.ToLower(model), "rerank") {
		return &dto.RerankRequest{
			Model:     model,
			Query:     "What is Deep Learning?",
			Documents: []any{"Deep Learning is a subset of machine learning.", "Machine learning is a field of artificial intelligence."},
			TopN:      lo.ToPtr(2),
		}
	}

	// 先判断是否为 Embedding 模型
	if strings.Contains(strings.ToLower(model), "embedding") ||
		strings.HasPrefix(model, "m3e") ||
		strings.Contains(model, "bge-") {
		// 返回 EmbeddingRequest
		return &dto.EmbeddingRequest{
			Model: model,
			Input: []any{"hello world"},
		}
	}

	// Responses compaction models (must use /v1/responses/compact)
	if strings.HasSuffix(model, ratio_setting.CompactModelSuffix) {
		return &dto.OpenAIResponsesCompactionRequest{
			Model: model,
			Input: defaultResponsesInput,
		}
	}

	// Responses-only models (e.g. codex series)
	if strings.Contains(strings.ToLower(model), "codex") {
		return &dto.OpenAIResponsesRequest{
			Model:           model,
			Input:           testResponsesInput,
			Stream:          lo.ToPtr(options.isStream),
			MaxOutputTokens: lo.Ternary(options.maxOutputTokens > 0, lo.ToPtr(options.maxOutputTokens), nil),
		}
	}

	// Chat/Completion 请求 - 返回 GeneralOpenAIRequest
	testRequest := &dto.GeneralOpenAIRequest{
		Model:  model,
		Stream: lo.ToPtr(options.isStream),
		Messages: []dto.Message{
			{
				Role:    "user",
				Content: userPrompt,
			},
		},
	}
	if options.isStream {
		testRequest.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}

	if options.maxOutputTokens > 0 {
		if dto.IsOpenAIReasoningOModel(model) {
			testRequest.MaxCompletionTokens = lo.ToPtr(options.maxOutputTokens)
		} else {
			testRequest.MaxTokens = lo.ToPtr(options.maxOutputTokens)
		}
	} else if dto.IsOpenAIReasoningOModel(model) {
		testRequest.MaxCompletionTokens = lo.ToPtr(uint(16))
	} else if strings.Contains(model, "thinking") {
		if !strings.Contains(model, "claude") {
			testRequest.MaxTokens = lo.ToPtr(uint(50))
		}
	} else if strings.Contains(model, "gemini") {
		testRequest.MaxTokens = lo.ToPtr(uint(3000))
	} else {
		testRequest.MaxTokens = lo.ToPtr(uint(16))
	}

	return testRequest
}

func TestChannel(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.CacheGetChannel(channelId)
	if err != nil {
		channel, err = model.GetChannelById(channelId, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	//defer func() {
	//	if channel.ChannelInfo.IsMultiKey {
	//		go func() { _ = channel.SaveChannelInfo() }()
	//	}
	//}()
	testModel := c.Query("model")
	endpointType := c.Query("endpoint_type")
	isStream, _ := strconv.ParseBool(c.Query("stream"))
	nativeProbe := c.Query("probe_mode") == "native" || c.Query("probe_case") != ""
	probeCase, err := parseProtocolProbeCase(c.Query("probe_case"), nativeProbe)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var keyIndex *int
	if rawKeyIndex, exists := c.GetQuery("key_index"); exists {
		parsedKeyIndex, parseErr := strconv.Atoi(rawKeyIndex)
		if parseErr != nil || parsedKeyIndex < 0 {
			common.ApiError(c, errors.New("key_index must be a non-negative integer"))
			return
		}
		keyIndex = &parsedKeyIndex
	}
	if keyIndex != nil && nativeProbe {
		common.ApiError(c, errors.New("key_index cannot be combined with probe_mode"))
		return
	}
	if nativeProbe && !dto.IsTextProtocolEndpointType(constant.EndpointType(endpointType)) {
		common.ApiError(c, errors.New("native probe requires a supported text endpoint_type"))
		return
	}
	probeExecution := resolveProtocolProbeExecution(
		channel,
		testModel,
		constant.EndpointType(endpointType),
		isStream,
		probeCase,
	)
	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tik := time.Now()
	requestCtx := context.Background()
	if c.Request != nil {
		requestCtx = c.Request.Context()
	}
	result := testChannel(requestCtx, channel, testUserID, channelTestOptions{
		model: testModel, endpointType: endpointType, isStream: isStream, nativeProbe: nativeProbe,
		protocolProbeCase: probeCase, protocolProbeExecution: probeExecution, keyIndex: keyIndex,
	})
	if nativeProbe {
		consumedTime := float64(time.Since(tik).Milliseconds()) / 1000.0
		classification := classifyNativeProbeResult(result)
		message := ""
		if result.localErr != nil {
			message = compactProbeError(result.localErr.Error())
		} else if result.newAPIError != nil {
			message = compactProbeError(result.newAPIError.Error())
		}
		c.JSON(http.StatusOK, gin.H{
			"success":              classification == "confirmed",
			"message":              message,
			"model":                testModel,
			"endpoint_type":        endpointType,
			"stream":               isStream,
			"http_status":          result.upstreamStatus,
			"time":                 consumedTime,
			"classification":       classification,
			"probe_case":           probeCase,
			"capability_level":     probeCase.CapabilityLevel(),
			"effective_route_mode": probeExecution.RouteMode,
			"recommended_mode": recommendedProtocolProbeMode(
				channel,
				testModel,
				constant.EndpointType(endpointType),
				isStream,
				probeCase,
				probeExecution,
				classification,
			),
		})
		return
	}
	respondChannelConnectionTest(c, channel, tik, result, channelTestResponseOptions{
		keyIndex:           keyIndex,
		updateResponseTime: keyIndex == nil,
	})
}

// shouldApplySystemPromptForConnectionTest mirrors the request rewriting guards used by real relay requests.
func shouldApplySystemPromptForConnectionTest(channel *model.Channel) bool {
	if channel == nil || model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		return false
	}
	return !channel.GetSetting().PassThroughBodyEnabled
}

// TestChannelConnectionPrompt uses the supplied prompt for one real channel connection test.
func TestChannelConnectionPrompt(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	request := channelConnectionTestRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	request.Model = strings.TrimSpace(request.Model)
	request.EndpointType = strings.TrimSpace(request.EndpointType)
	if request.Model == "" {
		common.ApiError(c, errors.New("model is required"))
		return
	}
	if len(request.Model) > 255 {
		common.ApiError(c, errors.New("invalid model"))
		return
	}
	if strings.TrimSpace(request.UserPrompt) == "" {
		common.ApiError(c, errors.New("user_prompt is required"))
		return
	}
	if len(request.UserPrompt) > maxChannelPromptTestUserPromptBytes {
		common.ApiError(c, errors.New("user_prompt must not exceed 16 KiB"))
		return
	}
	if request.EndpointType != "" {
		if _, ok := common.GetDefaultEndpointInfo(constant.EndpointType(request.EndpointType)); !ok {
			common.ApiError(c, errors.New("invalid endpoint_type"))
			return
		}
	}

	channel, err := model.CacheGetChannel(channelId)
	if err != nil {
		channel, err = model.GetChannelById(channelId, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	tik := time.Now()
	result := testChannel(c.Request.Context(), channel, testUserID, channelTestOptions{
		model:             request.Model,
		endpointType:      request.EndpointType,
		isStream:          request.Stream,
		userPrompt:        request.UserPrompt,
		maxOutputTokens:   getChannelConnectionTestMaxOutputTokens(request.UserPrompt),
		applySystemPrompt: shouldApplySystemPromptForConnectionTest(channel),
	})
	respondChannelConnectionTest(c, channel, tik, result, channelTestResponseOptions{
		includeDetails:     true,
		updateResponseTime: true,
	})
}

// respondChannelConnectionTest writes the shared success and failure response for connection tests.
func respondChannelConnectionTest(c *gin.Context, channel *model.Channel, tik time.Time, result testResult, options channelTestResponseOptions) {
	if options.keyIndex != nil {
		respondMultiKeyConnectionTest(c, tik, result, *options.keyIndex)
		return
	}
	if result.localErr != nil {
		resp := gin.H{
			"success": false,
			"message": result.localErr.Error(),
			"time":    0.0,
		}
		if result.newAPIError != nil {
			resp["error_code"] = result.newAPIError.GetErrorCode()
		}
		c.JSON(http.StatusOK, resp)
		return
	}
	tok := time.Now()
	milliseconds := tok.Sub(tik).Milliseconds()
	if options.updateResponseTime {
		go channel.UpdateResponseTime(milliseconds)
	}
	consumedTime := float64(milliseconds) / 1000.0
	if result.newAPIError != nil {
		c.JSON(http.StatusOK, gin.H{
			"success":    false,
			"message":    result.newAPIError.Error(),
			"time":       consumedTime,
			"error_code": result.newAPIError.GetErrorCode(),
		})
		return
	}
	response := gin.H{
		"success": true,
		"message": "",
		"time":    consumedTime,
	}
	if options.includeDetails {
		response["data"] = buildChannelTestResponseDetails(result)
	}
	c.JSON(http.StatusOK, response)
}

// respondMultiKeyConnectionTest writes the structured result consumed by the multi-key test queue.
func respondMultiKeyConnectionTest(c *gin.Context, tik time.Time, result testResult, keyIndex int) {
	milliseconds := time.Since(tik).Milliseconds()
	classification := classifyMultiKeyTestResult(result)
	response := gin.H{
		"success":        classification == "available",
		"message":        "",
		"key_index":      keyIndex,
		"classification": classification,
		"http_status":    result.upstreamStatus,
		"time":           float64(milliseconds) / 1000.0,
	}
	if result.newAPIError != nil {
		response["error_code"] = result.newAPIError.GetErrorCode()
		response["message"] = maskMultiKeyTestError(result, result.newAPIError.MaskSensitiveError())
	} else if result.localErr != nil {
		response["message"] = maskMultiKeyTestError(result, result.localErr.Error())
	}
	c.JSON(http.StatusOK, response)
}

// maskMultiKeyTestError prevents an upstream error from echoing the selected credential.
func maskMultiKeyTestError(result testResult, message string) string {
	message = common.MaskSensitiveInfo(message)
	if result.context == nil {
		return message
	}
	key := common.GetContextKeyString(result.context, constant.ContextKeyChannelKey)
	if key == "" {
		return message
	}
	return strings.ReplaceAll(message, key, "***")
}

// classifyMultiKeyTestResult maps connection failures to actionable key states.
func classifyMultiKeyTestResult(result testResult) string {
	if result.localErr == nil && result.newAPIError == nil && result.upstreamStatus >= http.StatusOK && result.upstreamStatus < http.StatusMultipleChoices {
		return "available"
	}
	if result.newAPIError != nil {
		switch result.newAPIError.GetErrorCode() {
		case types.ErrorCodeDoRequestFailed:
			return "network_error"
		case types.ErrorCodeInsufficientUserQuota, types.ErrorCodePreConsumeTokenQuotaFailed:
			return "quota_exhausted"
		case types.ErrorCodeModelNotFound:
			return "model_forbidden"
		case types.ErrorCodeGetChannelFailed, types.ErrorCodeChannelParamOverrideInvalid, types.ErrorCodeChannelHeaderOverrideInvalid, types.ErrorCodeChannelModelMappedError:
			return "configuration_error"
		}
		if classification := classifyMultiKeyErrorMessage(result.newAPIError.Error()); classification != "" {
			return classification
		}
		switch result.newAPIError.GetErrorCode() {
		case types.ErrorCodeBadResponse, types.ErrorCodeBadResponseBody, types.ErrorCodeEmptyResponse, types.ErrorCodeReadResponseBodyFailed:
			return "response_error"
		}
	}
	status := result.upstreamStatus
	if status == 0 && result.newAPIError != nil {
		status = result.newAPIError.StatusCode
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "auth_failed"
	case http.StatusPaymentRequired:
		return "quota_exhausted"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return "configuration_error"
	}
	if status >= http.StatusInternalServerError {
		return "upstream_error"
	}
	if status == 0 {
		return "network_error"
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return "response_error"
	}
	return "upstream_error"
}

// classifyMultiKeyErrorMessage recognizes actionable credential and quota failures whose HTTP status is ambiguous.
func classifyMultiKeyErrorMessage(message string) string {
	lowerMessage := strings.ToLower(message)
	quotaIndicators := []string{
		"quota exhausted",
		"quota_exhausted",
		"quota exceeded",
		"quota has been exhausted",
		"quota has been exceeded",
		"quota limit",
		"quota will be refreshed",
		"exceeded your quota",
		"usage limit",
		"billing limit",
		"credit limit",
		"credits exhausted",
		"out of credits",
		"insufficient credit",
		"insufficient balance",
		"balance is insufficient",
	}
	for _, indicator := range quotaIndicators {
		if strings.Contains(lowerMessage, indicator) {
			return "quota_exhausted"
		}
	}

	authIndicators := []string{
		"unable to verify your membership",
		"membership is active",
		"membership inactive",
		"subscription is active",
		"subscription inactive",
		"invalid api key",
		"incorrect api key",
		"expired api key",
		"revoked api key",
		"invalid token",
		"expired token",
		"revoked token",
		"authentication failed",
		"unauthorized",
	}
	for _, indicator := range authIndicators {
		if strings.Contains(lowerMessage, indicator) {
			return "auth_failed"
		}
	}
	return ""
}

func classifyNativeProbeResult(result testResult) string {
	if result.localErr == nil && result.newAPIError == nil && result.upstreamStatus == http.StatusOK {
		return "confirmed"
	}
	switch result.upstreamStatus {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return "path_mismatch"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "auth_error"
	case http.StatusTooManyRequests:
		return "rate_limited"
	}
	if result.upstreamStatus >= http.StatusInternalServerError {
		return "upstream_error"
	}
	if result.upstreamStatus == 0 {
		return "transport_error"
	}
	return "upstream_error"
}

func compactProbeError(message string) string {
	message = strings.TrimSpace(message)
	const maxProbeErrorRunes = 512
	runes := []rune(message)
	if len(runes) <= maxProbeErrorRunes {
		return message
	}
	return string(runes[:maxProbeErrorRunes])
}

// TestChannelPromptEffect 使用当前表单中的系统提示词发起一次非流式真实模型测试。
func TestChannelPromptEffect(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	request := channelPromptTestRequest{}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	request.Model = strings.TrimSpace(request.Model)
	request.UserPrompt = strings.TrimSpace(request.UserPrompt)
	if request.Model == "" || request.UserPrompt == "" || strings.TrimSpace(request.SystemPrompt) == "" {
		common.ApiError(c, errors.New("model, system_prompt and user_prompt are required"))
		return
	}
	if len(request.Model) > 255 || len(request.UserPrompt) > maxChannelPromptTestUserPromptBytes || len(request.SystemPrompt) > dto.MaxModelSystemPromptBytes {
		common.ApiError(c, errors.New("prompt test request exceeds the allowed size"))
		return
	}

	channel, err := model.CacheGetChannel(channelID)
	if err != nil {
		channel, err = model.GetChannelById(channelID, true)
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}

	testChannelConfig := *channel
	settings := testChannelConfig.GetSetting()
	if settings.PassThroughBodyEnabled || model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		common.ApiError(c, errors.New("request body passthrough is enabled; system prompts are not injected"))
		return
	}
	if settings.ModelSystemPrompts == nil {
		settings.ModelSystemPrompts = map[string]string{}
	}
	settings.ModelSystemPrompts[request.Model] = request.SystemPrompt
	if err := settings.ValidateSystemPrompts(); err != nil {
		common.ApiError(c, err)
		return
	}
	testChannelConfig.SetSetting(settings)

	testUserID, err := resolveChannelTestUserID(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tik := time.Now()
	result := testChannel(c.Request.Context(), &testChannelConfig, testUserID, channelTestOptions{
		model:                      request.Model,
		userPrompt:                 request.UserPrompt,
		maxOutputTokens:            512,
		applySystemPrompt:          true,
		requireSystemPromptSupport: true,
	})
	consumedTime := float64(time.Since(tik).Milliseconds()) / 1000.0
	if result.localErr != nil {
		response := gin.H{"success": false, "message": result.localErr.Error(), "time": consumedTime}
		if result.newAPIError != nil {
			response["error_code"] = result.newAPIError.GetErrorCode()
		}
		c.JSON(http.StatusOK, response)
		return
	}

	content := extractChannelTestResponseText(result.responseBody)
	if content == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "model returned no displayable text content",
			"time":    consumedTime,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"time":    consumedTime,
		"data":    gin.H{"content": content},
	})
}

// buildChannelTestResponseDetails builds the bounded display payload returned by the custom connection test.
func buildChannelTestResponseDetails(result testResult) channelTestResponseDetails {
	content, reasoningContent := extractChannelTestResponseContent(result.responseBody, result.isStream)
	sanitizedBody, _ := service.SanitizeConversationBody(result.responseBody)
	sanitizedBody, truncated := truncateChannelTestResponseDetail(sanitizedBody)

	return channelTestResponseDetails{
		EffectiveEndpointType: result.effectiveEndpointType,
		Stream:                result.isStream,
		Content:               content,
		ReasoningContent:      reasoningContent,
		RawResponse:           string(sanitizedBody),
		RawResponseTruncated:  result.responseBodyTruncated || truncated,
	}
}

// truncateChannelTestResponseDetail limits a display response without splitting a UTF-8 sequence.
func truncateChannelTestResponseDetail(body []byte) ([]byte, bool) {
	if len(body) <= maxChannelTestResponseDetailBytes {
		return body, false
	}

	end := maxChannelTestResponseDetailBytes
	for end > 0 && !utf8.Valid(body[:end]) {
		end--
	}
	return body[:end], true
}

// extractChannelTestResponseText extracts the final answer used by the system-prompt effect test.
func extractChannelTestResponseText(body []byte) string {
	content, _ := extractChannelTestResponseContent(body, false)
	return content
}

// extractChannelTestResponseContent extracts explicit answer and reasoning text from supported response protocols.
func extractChannelTestResponseContent(body []byte, isStream bool) (string, string) {
	if isStream {
		return extractChannelTestStreamContent(body)
	}
	return extractChannelTestJSONContent(gjson.ParseBytes(body))
}

// extractChannelTestJSONContent extracts one non-stream JSON response without mixing reasoning into the final answer.
func extractChannelTestJSONContent(result gjson.Result) (string, string) {
	if result.Get("choices").Exists() {
		contentParts := make([]string, 0)
		reasoningParts := make([]string, 0)
		appendChannelTestPart(&contentParts, result.Get("choices.0.text").String())
		appendChannelTestPart(&reasoningParts, result.Get("choices.0.message.reasoning_content").String())
		if len(reasoningParts) == 0 {
			appendChannelTestPart(&reasoningParts, result.Get("choices.0.message.reasoning").String())
		}

		chatContent := result.Get("choices.0.message.content")
		if chatContent.Type == gjson.String {
			appendChannelTestPart(&contentParts, chatContent.String())
		} else {
			for _, item := range chatContent.Array() {
				if item.Type == gjson.String {
					appendChannelTestPart(&contentParts, item.String())
					continue
				}
				appendChannelTestPart(&contentParts, item.Get("text").String())
			}
		}
		return strings.Join(contentParts, "\n"), strings.Join(reasoningParts, "\n")
	}

	if result.Get("output").Exists() || result.Get("output_text").Exists() {
		contentParts := make([]string, 0)
		reasoningParts := make([]string, 0)
		appendChannelTestPart(&contentParts, result.Get("output_text").String())
		hasOutputText := len(contentParts) > 0

		for _, output := range result.Get("output").Array() {
			outputType := output.Get("type").String()
			for _, summary := range output.Get("summary").Array() {
				appendChannelTestPart(&reasoningParts, summary.Get("text").String())
			}
			if outputType == "reasoning" {
				appendChannelTestPart(&reasoningParts, output.Get("summary_text").String())
				appendChannelTestPart(&reasoningParts, output.Get("text").String())
			}
			for _, item := range output.Get("content").Array() {
				itemType := item.Get("type").String()
				if outputType == "reasoning" || strings.Contains(itemType, "reasoning") || strings.Contains(itemType, "summary") {
					appendChannelTestPart(&reasoningParts, item.Get("text").String())
					continue
				}
				if !hasOutputText && (itemType == "" || itemType == "text" || itemType == "output_text") {
					appendChannelTestPart(&contentParts, item.Get("text").String())
				}
			}
		}
		return strings.Join(contentParts, "\n"), strings.Join(reasoningParts, "\n")
	}

	if result.Get("candidates").Exists() {
		contentParts := make([]string, 0)
		reasoningParts := make([]string, 0)
		for _, item := range result.Get("candidates.0.content.parts").Array() {
			if item.Get("thought").Bool() {
				appendChannelTestPart(&reasoningParts, item.Get("text").String())
				continue
			}
			appendChannelTestPart(&contentParts, item.Get("text").String())
		}
		return strings.Join(contentParts, "\n"), strings.Join(reasoningParts, "\n")
	}

	contentParts := make([]string, 0)
	reasoningParts := make([]string, 0)
	for _, item := range result.Get("content").Array() {
		switch item.Get("type").String() {
		case "thinking":
			appendChannelTestPart(&reasoningParts, item.Get("thinking").String())
		case "text", "":
			appendChannelTestPart(&contentParts, item.Get("text").String())
		}
	}
	return strings.Join(contentParts, "\n"), strings.Join(reasoningParts, "\n")
}

// extractChannelTestStreamContent aggregates explicit text deltas from an SSE response.
func extractChannelTestStreamContent(body []byte) (string, string) {
	var content strings.Builder
	var reasoning strings.Builder
	var terminalContent string
	var terminalReasoning string

	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}

		event := gjson.ParseBytes(payload)
		switch event.Get("type").String() {
		case "response.output_text.delta":
			appendChannelTestDelta(&content, event.Get("delta").String())
			continue
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			appendChannelTestDelta(&reasoning, event.Get("delta").String())
			continue
		case "response.output_text.done", "response.reasoning_summary_text.done", "response.reasoning_text.done":
			continue
		case "response.completed":
			terminalContent, terminalReasoning = extractChannelTestJSONContent(event.Get("response"))
			continue
		}

		appendChannelTestDelta(&content, event.Get("choices.0.delta.content").String())
		reasoningDelta := event.Get("choices.0.delta.reasoning_content").String()
		if strings.TrimSpace(reasoningDelta) == "" {
			reasoningDelta = event.Get("choices.0.delta.reasoning").String()
		}
		appendChannelTestDelta(&reasoning, reasoningDelta)

		deltaType := event.Get("delta.type").String()
		switch deltaType {
		case "text_delta":
			appendChannelTestDelta(&content, event.Get("delta.text").String())
		case "thinking_delta":
			appendChannelTestDelta(&reasoning, event.Get("delta.thinking").String())
		}

		contentBlock := event.Get("content_block")
		switch contentBlock.Get("type").String() {
		case "text":
			appendChannelTestDelta(&content, contentBlock.Get("text").String())
		case "thinking":
			appendChannelTestDelta(&reasoning, contentBlock.Get("thinking").String())
		}

		for _, item := range event.Get("candidates.0.content.parts").Array() {
			if item.Get("thought").Bool() {
				appendChannelTestDelta(&reasoning, item.Get("text").String())
				continue
			}
			appendChannelTestDelta(&content, item.Get("text").String())
		}
	}

	if content.Len() == 0 {
		content.WriteString(terminalContent)
	}
	if reasoning.Len() == 0 {
		reasoning.WriteString(terminalReasoning)
	}
	return content.String(), reasoning.String()
}

// appendChannelTestPart appends one non-empty response part while preserving its original whitespace.
func appendChannelTestPart(parts *[]string, value string) {
	if strings.TrimSpace(value) != "" {
		*parts = append(*parts, value)
	}
}

// appendChannelTestDelta appends one non-empty streaming delta without inserting protocol-visible separators.
func appendChannelTestDelta(builder *strings.Builder, value string) {
	if value != "" {
		builder.WriteString(value)
	}
}

// channelTestSummary records the outcome of one channel test cycle so the
// system task can persist a per-run result for history.
type channelTestSummary struct {
	Tested    int `json:"tested"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Disabled  int `json:"disabled"`
	Enabled   int `json:"enabled"`
}

// channelTestTarget identifies either a channel or one exact multi-key entry to probe.
type channelTestTarget struct {
	channel  *model.Channel
	keyIndex *int
}

// performChannelTests runs the channel test loop synchronously, honoring ctx
// cancellation so a system-task runner that loses its lease stops promptly. When
// report is non-nil it is called after each probe with (processed, total) so
// the system task can surface progress.
func performChannelTests(ctx context.Context, targets []channelTestTarget, testUserID int, allowDisable bool, report func(processed, total int)) channelTestSummary {
	summary := channelTestSummary{}
	var disableThreshold = int64(common.ChannelDisableThreshold * 1000)
	if disableThreshold == 0 {
		disableThreshold = 10000000 // a impossible value
	}

	total := len(targets)
	for index, target := range targets {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		if report != nil {
			report(index, total) // probes completed before this one
		}
		channel := target.channel
		testedStatus := channel.Status
		if target.keyIndex != nil {
			testedStatus = common.ChannelStatusAutoDisabled
		}
		isChannelEnabled := testedStatus == common.ChannelStatusEnabled
		tik := time.Now()
		result := testChannel(ctx, channel, testUserID, channelTestOptions{
			isStream: shouldUseStreamForAutomaticChannelTest(channel),
			keyIndex: target.keyIndex,
		})
		tok := time.Now()
		milliseconds := tok.Sub(tik).Milliseconds()
		if ctx != nil && ctx.Err() != nil {
			break
		}

		summary.Tested++

		shouldBanChannel := false
		newAPIError := result.newAPIError
		// request error disables the channel
		if newAPIError != nil {
			shouldBanChannel = service.ShouldDisableChannel(result.newAPIError)
		}

		// 当错误检查通过，才检查响应时间
		if common.AutomaticDisableChannelEnabled && !shouldBanChannel {
			if milliseconds > disableThreshold {
				err := fmt.Errorf("响应时间 %.2fs 超过阈值 %.2fs", float64(milliseconds)/1000.0, float64(disableThreshold)/1000.0)
				newAPIError = types.NewOpenAIError(err, types.ErrorCodeChannelResponseTimeExceeded, http.StatusRequestTimeout)
				shouldBanChannel = true
			}
		}

		if newAPIError == nil {
			summary.Succeeded++
		} else {
			summary.Failed++
		}
		if target.keyIndex != nil && (newAPIError != nil || result.localErr != nil) {
			errorCode := types.ErrorCode("")
			errorType := types.ErrorType("local_error")
			statusCode := 0
			if newAPIError != nil {
				errorCode = newAPIError.GetErrorCode()
				errorType = newAPIError.GetErrorType()
				statusCode = newAPIError.StatusCode
			}
			common.SysLog(fmt.Sprintf("multi-key passive recovery failed: channel_id=%d, key_index=%d, error_code=%s, error_type=%s, status_code=%d", channel.Id, *target.keyIndex, errorCode, errorType, statusCode))
		}

		// disable channel
		if allowDisable && isChannelEnabled && shouldBanChannel && channel.GetAutoBan() {
			processChannelError(result.context, *types.NewChannelError(channel.Id, channel.Type, channel.Name, channel.ChannelInfo.IsMultiKey, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.GetAutoBan()), newAPIError)
			recordRelayErrorLog(result.context, nil, newAPIError, "", nil, false)
			summary.Disabled++
		}

		// enable channel
		if result.localErr == nil && !isChannelEnabled && service.ShouldEnableChannel(newAPIError, testedStatus) {
			if service.EnableChannel(channel.Id, common.GetContextKeyString(result.context, constant.ContextKeyChannelKey), channel.Name) {
				summary.Enabled++
			}
		}

		channel.UpdateResponseTime(milliseconds)
		if common.RequestInterval > 0 {
			if ctx == nil {
				time.Sleep(common.RequestInterval)
			} else {
				select {
				case <-ctx.Done():
					return summary
				case <-time.After(common.RequestInterval):
				}
			}
		}
	}
	if report != nil && (ctx == nil || ctx.Err() == nil) {
		report(total, total) // mark complete only when the full set was tested
	}
	return summary
}

// runChannelTestTask runs one synchronous channel test cycle for the system task
// runner (both the scheduled job and the manual "test all channels" trigger go
// through here). It honors ctx cancellation so a runner that loses its lease
// stops promptly. mode selects the channel set: an empty mode falls back to the
// configured monitor ChannelTestMode (scheduled behavior), while a manual
// trigger passes ChannelTestModeScheduledAll to test every channel. When notify
// is set the root user is notified on completion. Cross-instance execution is
// guarded by the system task per-type lock, so no process-local guard is needed.
func runChannelTestTask(ctx context.Context, mode string, notify bool, report func(processed, total int)) (channelTestSummary, error) {
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		return channelTestSummary{}, err
	}
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		return channelTestSummary{}, err
	}
	if strings.TrimSpace(mode) == "" {
		mode = operation_setting.GetMonitorSetting().ChannelTestMode
	}
	targets := buildChannelTestTargets(channels, mode)
	allowDisable := mode != operation_setting.ChannelTestModePassiveRecovery
	summary := performChannelTests(ctx, targets, testUserID, allowDisable, report)
	if notify && (ctx == nil || ctx.Err() == nil) {
		service.NotifyRootUser(dto.NotifyTypeChannelTest, "通道测试完成", "所有通道测试已完成")
	}
	return summary, nil
}

// buildChannelTestTargets expands passive recovery into one probe per auto-disabled key.
func buildChannelTestTargets(channels []*model.Channel, mode string) []channelTestTarget {
	targets := make([]channelTestTarget, 0, len(channels))
	for _, channel := range channels {
		if channel.Status == common.ChannelStatusManuallyDisabled {
			continue
		}
		if mode != operation_setting.ChannelTestModePassiveRecovery {
			targets = append(targets, channelTestTarget{channel: channel})
			continue
		}
		if !channel.ChannelInfo.IsMultiKey {
			if channel.Status == common.ChannelStatusAutoDisabled {
				targets = append(targets, channelTestTarget{channel: channel})
			}
			continue
		}
		for keyIndex := range channel.GetKeys() {
			if channel.ChannelInfo.MultiKeyStatusList[keyIndex] != common.ChannelStatusAutoDisabled {
				continue
			}
			index := keyIndex
			targets = append(targets, channelTestTarget{channel: channel, keyIndex: &index})
		}
	}
	return targets
}

// TestAllChannels enqueues a channel_test system task instead of running the
// test loop inline. If any channel_test task is already active, the manual run is
// rejected so the caller does not mistake a scheduled run for this manual one.
func TestAllChannels(c *gin.Context) {
	task, created, err := service.EnqueueSystemTask(model.SystemTaskTypeChannelTest, channelTestTaskPayload{
		Mode:   operation_setting.ChannelTestModeScheduledAll,
		Notify: true,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !created {
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": "已有通道测试任务正在运行或等待中，不能启动本次手动任务",
			"data": gin.H{
				"task_id": task.TaskID,
				"status":  task.Status,
				"type":    task.Type,
			},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"task_id": task.TaskID,
			"status":  task.Status,
		},
	})
}
