package controller

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTestRequestUsesCustomUserPromptAndOutputLimit(t *testing.T) {
	options := channelTestOptions{
		userPrompt:      "你好 \"new-api\"",
		maxOutputTokens: 512,
	}

	chatRequest, ok := buildTestRequest("gpt-4o-mini", "", nil, options).(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chatRequest.Messages, 1)
	assert.Equal(t, options.userPrompt, chatRequest.Messages[0].StringContent())
	assert.Equal(t, uint(512), *chatRequest.MaxTokens)

	responsesRequest, ok := buildTestRequest("codex-mini", "", nil, options).(*dto.OpenAIResponsesRequest)
	require.True(t, ok)
	var input []map[string]string
	require.NoError(t, common.Unmarshal(responsesRequest.Input, &input))
	require.Len(t, input, 1)
	assert.Equal(t, options.userPrompt, input[0]["content"])
	assert.Equal(t, uint(512), *responsesRequest.MaxOutputTokens)

	explicitResponsesRequest, ok := buildTestRequest(
		"gpt-4o-mini",
		string(constant.EndpointTypeOpenAIResponse),
		nil,
		options,
	).(*dto.OpenAIResponsesRequest)
	require.True(t, ok)
	require.NoError(t, common.Unmarshal(explicitResponsesRequest.Input, &input))
	assert.Equal(t, options.userPrompt, input[0]["content"])
}

// TestResolveChannelTestProtocolMatrix verifies explicit selections and automatic model routing share one endpoint contract.
func TestResolveChannelTestProtocolMatrix(t *testing.T) {
	tests := []struct {
		name             string
		channelType      int
		modelName        string
		selectedEndpoint string
		wantPath         string
		wantEndpoint     constant.EndpointType
		wantStream       bool
	}{
		{name: "auto chat", channelType: constant.ChannelTypeOpenAI, modelName: "gpt-4o-mini", wantPath: "/v1/chat/completions", wantEndpoint: constant.EndpointTypeOpenAI, wantStream: true},
		{name: "explicit chat", channelType: constant.ChannelTypeOpenAI, modelName: "text-model", selectedEndpoint: string(constant.EndpointTypeOpenAI), wantPath: "/v1/chat/completions", wantEndpoint: constant.EndpointTypeOpenAI, wantStream: true},
		{name: "explicit responses", channelType: constant.ChannelTypeOpenAI, modelName: "text-model", selectedEndpoint: string(constant.EndpointTypeOpenAIResponse), wantPath: "/v1/responses", wantEndpoint: constant.EndpointTypeOpenAIResponse, wantStream: true},
		{name: "explicit compact", channelType: constant.ChannelTypeOpenAI, modelName: "text-model", selectedEndpoint: string(constant.EndpointTypeOpenAIResponseCompact), wantPath: "/v1/responses/compact", wantEndpoint: constant.EndpointTypeOpenAIResponseCompact},
		{name: "explicit anthropic", channelType: constant.ChannelTypeOpenAI, modelName: "text-model", selectedEndpoint: string(constant.EndpointTypeAnthropic), wantPath: "/v1/messages", wantEndpoint: constant.EndpointTypeAnthropic, wantStream: true},
		{name: "explicit gemini", channelType: constant.ChannelTypeOpenAI, modelName: "text-model", selectedEndpoint: string(constant.EndpointTypeGemini), wantPath: "/v1beta/models/{model}:generateContent", wantEndpoint: constant.EndpointTypeGemini, wantStream: true},
		{name: "explicit rerank", channelType: constant.ChannelTypeOpenAI, modelName: "rerank-model", selectedEndpoint: string(constant.EndpointTypeJinaRerank), wantPath: "/v1/rerank", wantEndpoint: constant.EndpointTypeJinaRerank},
		{name: "explicit image", channelType: constant.ChannelTypeOpenAI, modelName: "image-model", selectedEndpoint: string(constant.EndpointTypeImageGeneration), wantPath: "/v1/images/generations", wantEndpoint: constant.EndpointTypeImageGeneration},
		{name: "explicit embeddings", channelType: constant.ChannelTypeOpenAI, modelName: "embedding-model", selectedEndpoint: string(constant.EndpointTypeEmbeddings), wantPath: "/v1/embeddings", wantEndpoint: constant.EndpointTypeEmbeddings},
		{name: "auto codex channel", channelType: constant.ChannelTypeCodex, modelName: "gpt-5", wantPath: "/v1/responses", wantEndpoint: constant.EndpointTypeOpenAIResponse, wantStream: true},
		{name: "auto codex model", channelType: constant.ChannelTypeOpenAI, modelName: "codex-mini", wantPath: "/v1/responses", wantEndpoint: constant.EndpointTypeOpenAIResponse, wantStream: true},
		{name: "auto compact suffix", channelType: constant.ChannelTypeOpenAI, modelName: "gpt-5" + ratio_setting.CompactModelSuffix, wantPath: "/v1/responses/compact", wantEndpoint: constant.EndpointTypeOpenAIResponseCompact},
		{name: "auto rerank", channelType: constant.ChannelTypeOpenAI, modelName: "jina-rerank-v2", wantPath: "/v1/rerank", wantEndpoint: constant.EndpointTypeJinaRerank},
		{name: "auto embedding", channelType: constant.ChannelTypeOpenAI, modelName: "text-embedding-3-small", wantPath: "/v1/embeddings", wantEndpoint: constant.EndpointTypeEmbeddings},
		{name: "auto moka embedding", channelType: constant.ChannelTypeMokaAI, modelName: "moka-model", wantPath: "/v1/embeddings", wantEndpoint: constant.EndpointTypeEmbeddings},
		{name: "auto volcengine seedream", channelType: constant.ChannelTypeVolcEngine, modelName: "doubao-seedream-4", wantPath: "/v1/images/generations", wantEndpoint: constant.EndpointTypeImageGeneration},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel := &model.Channel{Type: test.channelType}
			normalizedEndpoint := normalizeChannelTestEndpoint(channel, test.modelName, test.selectedEndpoint)
			requestPath := resolveChannelTestRequestPath(channel, test.modelName, normalizedEndpoint)
			actualEndpoint := resolveChannelTestEndpointType(requestPath)

			assert.Equal(t, test.wantPath, requestPath)
			assert.Equal(t, test.wantEndpoint, actualEndpoint)
			assert.Equal(t, test.wantStream, channelTestEndpointSupportsStream(actualEndpoint))
		})
	}

	assert.Equal(
		t,
		constant.EndpointTypeGemini,
		resolveChannelTestEndpointType("/v1/models/test:streamGenerateContent?alt=sse"),
	)
}

// TestChannelTestRejectsUnsupportedStreamingBeforeSetup verifies structured endpoints stop before user or upstream access.
func TestChannelTestRejectsUnsupportedStreamingBeforeSetup(t *testing.T) {
	tests := []constant.EndpointType{
		constant.EndpointTypeOpenAIResponseCompact,
		constant.EndpointTypeJinaRerank,
		constant.EndpointTypeImageGeneration,
		constant.EndpointTypeEmbeddings,
	}

	for _, endpointType := range tests {
		t.Run(string(endpointType), func(t *testing.T) {
			result := testChannel(t.Context(), &model.Channel{
				Type:   constant.ChannelTypeOpenAI,
				Models: "test-model",
			}, 1, channelTestOptions{
				model:        "test-model",
				endpointType: string(endpointType),
				isStream:     true,
			})

			require.Error(t, result.localErr)
			require.NotNil(t, result.newAPIError)
			assert.Equal(t, types.ErrorCodeInvalidRequest, result.newAPIError.GetErrorCode())
			assert.Equal(t, endpointType, result.effectiveEndpointType)
			assert.Contains(t, result.localErr.Error(), "only accepts non-streaming channel tests")
		})
	}
}

// TestChannelTestStreamingRequestsBuildMatchingRelayInfo verifies all streaming text DTOs remain the transport source of truth.
func TestChannelTestStreamingRequestsBuildMatchingRelayInfo(t *testing.T) {
	tests := []struct {
		name         string
		endpointType constant.EndpointType
		relayFormat  types.RelayFormat
		requestPath  string
	}{
		{name: "openai chat", endpointType: constant.EndpointTypeOpenAI, relayFormat: types.RelayFormatOpenAI, requestPath: "/v1/chat/completions"},
		{name: "openai responses", endpointType: constant.EndpointTypeOpenAIResponse, relayFormat: types.RelayFormatOpenAIResponses, requestPath: "/v1/responses"},
		{name: "anthropic", endpointType: constant.EndpointTypeAnthropic, relayFormat: types.RelayFormatClaude, requestPath: "/v1/messages"},
		{name: "gemini", endpointType: constant.EndpointTypeGemini, relayFormat: types.RelayFormatGemini, requestPath: "/v1beta/models/test:streamGenerateContent?alt=sse"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, test.requestPath, nil)
			request := buildTestRequest("test-model", string(test.endpointType), nil, channelTestOptions{isStream: true})

			info, err := relaycommon.GenRelayInfo(ctx, test.relayFormat, request, nil)

			require.NoError(t, err)
			assert.True(t, info.IsStream)
			assert.True(t, request.IsStream(ctx))
		})
	}
}

// TestChannelConnectionTestOutputLimit verifies default and custom prompt output limits.
func TestChannelConnectionTestOutputLimit(t *testing.T) {
	assert.Zero(t, getChannelConnectionTestMaxOutputTokens("hi"))
	assert.Equal(t, uint(1024), getChannelConnectionTestMaxOutputTokens("Hi"))
	assert.Equal(t, uint(1024), getChannelConnectionTestMaxOutputTokens("hi "))
	assert.Equal(t, uint(1024), getChannelConnectionTestMaxOutputTokens("first line\nsecond line"))

	defaultRequest, ok := buildTestRequest("gpt-4o-mini", "", nil, channelTestOptions{
		userPrompt:      "hi",
		maxOutputTokens: getChannelConnectionTestMaxOutputTokens("hi"),
	}).(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, defaultRequest.MaxTokens)
	assert.Equal(t, uint(16), *defaultRequest.MaxTokens)

	customPrompt := "  first line\nsecond line  "
	customRequest, ok := buildTestRequest("gpt-4o-mini", "", nil, channelTestOptions{
		userPrompt:      customPrompt,
		maxOutputTokens: getChannelConnectionTestMaxOutputTokens(customPrompt),
	}).(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, customRequest.Messages, 1)
	assert.Equal(t, customPrompt, customRequest.Messages[0].StringContent())
	require.NotNil(t, customRequest.MaxTokens)
	assert.Equal(t, uint(1024), *customRequest.MaxTokens)

	reasoningRequest, ok := buildTestRequest("o3-mini", "", nil, channelTestOptions{
		userPrompt:      "custom",
		maxOutputTokens: customChannelTestMaxOutputTokens,
	}).(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, reasoningRequest.MaxCompletionTokens)
	assert.Equal(t, uint(1024), *reasoningRequest.MaxCompletionTokens)

	explicitReasoningRequest, ok := buildTestRequest(
		"o3-mini",
		string(constant.EndpointTypeOpenAI),
		nil,
		channelTestOptions{userPrompt: "custom", maxOutputTokens: customChannelTestMaxOutputTokens},
	).(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Nil(t, explicitReasoningRequest.MaxTokens)
	require.NotNil(t, explicitReasoningRequest.MaxCompletionTokens)
	assert.Equal(t, uint(1024), *explicitReasoningRequest.MaxCompletionTokens)

	responsesRequest, ok := buildTestRequest("codex-mini", "", nil, channelTestOptions{
		userPrompt:      "custom",
		maxOutputTokens: customChannelTestMaxOutputTokens,
	}).(*dto.OpenAIResponsesRequest)
	require.True(t, ok)
	require.NotNil(t, responsesRequest.MaxOutputTokens)
	assert.Equal(t, uint(1024), *responsesRequest.MaxOutputTokens)

	compactRequest, ok := buildTestRequest(
		"gpt-4o-mini",
		string(constant.EndpointTypeOpenAIResponseCompact),
		nil,
		channelTestOptions{userPrompt: "custom", maxOutputTokens: customChannelTestMaxOutputTokens},
	).(*dto.OpenAIResponsesCompactionRequest)
	require.True(t, ok)
	var compactInput []map[string]string
	require.NoError(t, common.Unmarshal(compactRequest.Input, &compactInput))
	require.Len(t, compactInput, 1)
	assert.Equal(t, "hi", compactInput[0]["content"])

	for _, endpointType := range []constant.EndpointType{
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini,
	} {
		request, requestOk := buildTestRequest(
			"text-model",
			string(endpointType),
			nil,
			channelTestOptions{userPrompt: customPrompt, maxOutputTokens: customChannelTestMaxOutputTokens},
		).(*dto.GeneralOpenAIRequest)
		require.True(t, requestOk)
		require.Len(t, request.Messages, 1)
		assert.Equal(t, customPrompt, request.Messages[0].StringContent())
		require.NotNil(t, request.MaxTokens)
		assert.Equal(t, uint(1024), *request.MaxTokens)
	}

	embeddingRequest, ok := buildTestRequest(
		"embedding-model",
		string(constant.EndpointTypeEmbeddings),
		nil,
		channelTestOptions{userPrompt: "custom", maxOutputTokens: customChannelTestMaxOutputTokens},
	).(*dto.EmbeddingRequest)
	require.True(t, ok)
	assert.Equal(t, []any{"hello world"}, embeddingRequest.Input)

	rerankRequest, ok := buildTestRequest(
		"rerank-model",
		string(constant.EndpointTypeJinaRerank),
		nil,
		channelTestOptions{userPrompt: "custom", maxOutputTokens: customChannelTestMaxOutputTokens},
	).(*dto.RerankRequest)
	require.True(t, ok)
	assert.Equal(t, "What is Deep Learning?", rerankRequest.Query)

	imageRequest, ok := buildTestRequest(
		"image-model",
		string(constant.EndpointTypeImageGeneration),
		nil,
		channelTestOptions{userPrompt: "custom", maxOutputTokens: customChannelTestMaxOutputTokens},
	).(*dto.ImageRequest)
	require.True(t, ok)
	assert.Equal(t, "a cute cat", imageRequest.Prompt)
}

// TestChannelConnectionPromptValidatesInput verifies malformed tests stop before channel lookup.
func TestChannelConnectionPromptValidatesInput(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantMessage string
	}{
		{
			name:        "blank model",
			body:        `{"model":"   ","user_prompt":"hi"}`,
			wantMessage: "model is required",
		},
		{
			name:        "oversized model",
			body:        `{"model":"` + strings.Repeat("m", 256) + `","user_prompt":"hi"}`,
			wantMessage: "invalid model",
		},
		{
			name:        "blank prompt",
			body:        `{"model":"gpt-4o-mini","user_prompt":"   "}`,
			wantMessage: "user_prompt is required",
		},
		{
			name:        "oversized prompt",
			body:        `{"model":"gpt-4o-mini","user_prompt":"` + strings.Repeat("a", maxChannelPromptTestUserPromptBytes+1) + `"}`,
			wantMessage: "user_prompt must not exceed 16 KiB",
		},
		{
			name:        "invalid endpoint",
			body:        `{"model":"gpt-4o-mini","user_prompt":"hi","endpoint_type":"unknown"}`,
			wantMessage: "invalid endpoint_type",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Params = gin.Params{{Key: "id", Value: "1"}}
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/test/1/connection", strings.NewReader(test.body))
			ctx.Request.Header.Set("Content-Type", "application/json")

			TestChannelConnectionPrompt(ctx)

			assert.Contains(t, response.Body.String(), test.wantMessage)
		})
	}
}

// TestChannelConnectionTestAppliesSavedSystemPrompt verifies the connection dialog sends the same rewritten text request as the relay path.
func TestChannelConnectionTestAppliesSavedSystemPrompt(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	service.InitHttpClient()
	testUser := &model.User{
		Id:       1,
		Username: "channel-test-user",
		Password: "channel-test-password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Quota:    1_000_000,
		Group:    "default",
	}
	testUser.SetSetting(dto.UserSetting{AcceptUnsetRatioModel: true})
	require.NoError(t, db.Create(testUser).Error)

	originalLogConsumeEnabled := common.LogConsumeEnabled
	originalGlobalPassThrough := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	common.LogConsumeEnabled = false
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() {
		common.LogConsumeEnabled = originalLogConsumeEnabled
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = originalGlobalPassThrough
	})

	tests := []struct {
		name              string
		settings          dto.ChannelSettings
		globalPassThrough bool
		wantSystemPrompt  string
	}{
		{
			name: "model prompt matches requested model before mapping",
			settings: dto.ChannelSettings{
				SystemPrompt:         "channel default",
				SystemPromptOverride: true,
				ModelSystemPrompts:   map[string]string{"gpt-4o-mini": "requested model prompt"},
			},
			wantSystemPrompt: "requested model prompt",
		},
		{
			name:             "channel default is used without model prompt",
			settings:         dto.ChannelSettings{SystemPrompt: "channel default"},
			wantSystemPrompt: "channel default",
		},
		{
			name:     "request remains unchanged without prompt",
			settings: dto.ChannelSettings{},
		},
		{
			name:     "channel passthrough skips prompt",
			settings: dto.ChannelSettings{SystemPrompt: "channel default", PassThroughBodyEnabled: true},
		},
		{
			name:              "global passthrough skips prompt",
			settings:          dto.ChannelSettings{SystemPrompt: "channel default"},
			globalPassThrough: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model_setting.GetGlobalSettings().PassThroughRequestEnabled = test.globalPassThrough
			capturedBody := make(chan []byte, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				body, _ := io.ReadAll(request.Body)
				capturedBody <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
			}))
			defer upstream.Close()

			mapping := `{"gpt-4o-mini":"gpt-4o"}`
			baseURL := upstream.URL
			channel := &model.Channel{
				Id:           1,
				Type:         constant.ChannelTypeOpenAI,
				Key:          "test-key",
				Status:       common.ChannelStatusEnabled,
				Name:         "system prompt test",
				BaseURL:      &baseURL,
				Models:       "gpt-4o-mini",
				Group:        "default",
				ModelMapping: &mapping,
				CreatedTime:  common.GetTimestamp(),
			}
			channel.SetSetting(test.settings)

			result := testChannel(t.Context(), channel, 1, channelTestOptions{
				model:             "gpt-4o-mini",
				userPrompt:        "hi",
				applySystemPrompt: shouldApplySystemPromptForConnectionTest(channel),
			})

			require.NoError(t, result.localErr)
			require.Nil(t, result.newAPIError)
			body := <-capturedBody
			var upstreamRequest struct {
				Model    string `json:"model"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			require.NoError(t, common.Unmarshal(body, &upstreamRequest))
			assert.Equal(t, "gpt-4o", upstreamRequest.Model)

			systemPrompts := make([]string, 0, 1)
			for _, message := range upstreamRequest.Messages {
				if message.Role == "system" {
					systemPrompts = append(systemPrompts, message.Content)
				}
			}
			if test.wantSystemPrompt == "" {
				assert.Empty(t, systemPrompts)
			} else {
				assert.Equal(t, []string{test.wantSystemPrompt}, systemPrompts)
			}
		})
	}
}

// TestChannelTestCommitsUnmatchedResponseOverride verifies the test recorder receives the buffered client response.
func TestChannelTestCommitsUnmatchedResponseOverride(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	service.InitHttpClient()
	testUser := &model.User{
		Id:       1,
		Username: "channel-buffer-test-user",
		Password: "channel-buffer-test-password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Quota:    1_000_000,
		Group:    "default",
	}
	testUser.SetSetting(dto.UserSetting{AcceptUnsetRatioModel: true})
	require.NoError(t, db.Create(testUser).Error)

	originalLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		common.LogConsumeEnabled = originalLogConsumeEnabled
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-buffer","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"visible answer","reasoning_content":"visible reasoning"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`)
	}))
	defer upstream.Close()

	baseURL := upstream.URL
	paramOverride := common.GetJsonString(responseOverrideForControllerTest())
	channel := &model.Channel{
		Id:            1,
		Type:          constant.ChannelTypeOpenAI,
		Key:           "test-key",
		Status:        common.ChannelStatusEnabled,
		Name:          "response buffer channel test",
		BaseURL:       &baseURL,
		Models:        "gpt-4o-mini",
		Group:         "default",
		ParamOverride: &paramOverride,
		CreatedTime:   common.GetTimestamp(),
	}

	result := testChannel(t.Context(), channel, 1, channelTestOptions{
		model:      "gpt-4o-mini",
		userPrompt: "hi",
	})

	require.NoError(t, result.localErr)
	require.Nil(t, result.newAPIError)
	assert.NotEmpty(t, result.responseBody)
	assert.Equal(t, constant.EndpointTypeOpenAI, result.effectiveEndpointType)
	assert.False(t, result.isStream)
	assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(result.context))
	details := buildChannelTestResponseDetails(result)
	assert.Equal(t, "visible answer", details.Content)
	assert.Equal(t, "visible reasoning", details.ReasoningContent)
	assert.Contains(t, details.RawResponse, "visible answer")
}

// TestChannelTestResponseOverrideMatchStillRecordsUsage verifies a rejected client result remains one billable upstream test.
func TestChannelTestResponseOverrideMatchStillRecordsUsage(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	service.InitHttpClient()
	testUser := &model.User{
		Id:       1,
		Username: "channel-match-test-user",
		Password: "channel-match-test-password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Quota:    1_000_000,
		Group:    "default",
	}
	testUser.SetSetting(dto.UserSetting{AcceptUnsetRatioModel: true})
	require.NoError(t, db.Create(testUser).Error)

	originalLogConsumeEnabled := common.LogConsumeEnabled
	originalDataExportEnabled := common.DataExportEnabled
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
	t.Cleanup(func() {
		common.LogConsumeEnabled = originalLogConsumeEnabled
		common.DataExportEnabled = originalDataExportEnabled
	})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-rejected","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	}))
	defer upstream.Close()

	baseURL := upstream.URL
	paramOverride := common.GetJsonString(responseOverrideForControllerTest())
	channel := &model.Channel{
		Id:            91,
		Type:          constant.ChannelTypeOpenAI,
		Key:           "test-key",
		Status:        common.ChannelStatusEnabled,
		Name:          "response override match channel test",
		BaseURL:       &baseURL,
		Models:        "gpt-4o-mini",
		Group:         "default",
		ParamOverride: &paramOverride,
		CreatedTime:   common.GetTimestamp(),
	}

	result := testChannel(t.Context(), channel, 1, channelTestOptions{
		model:      "gpt-4o-mini",
		userPrompt: "hi",
	})

	require.Error(t, result.localErr)
	require.NotNil(t, result.newAPIError)
	assert.Empty(t, result.responseBody)
	assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(result.context))
	var consumeLogs []model.Log
	require.NoError(t, db.Where("channel_id = ?", channel.Id).Find(&consumeLogs).Error)
	require.Len(t, consumeLogs, 1)
	assert.Equal(t, 2, consumeLogs[0].PromptTokens)
	assert.Equal(t, 1, consumeLogs[0].CompletionTokens)
	assert.Equal(t, "模型测试", consumeLogs[0].Content)
}

// TestChannelTestFinalClientResponseMatrix verifies every dialog endpoint and supported stream shape traverses a real upstream relay.
func TestChannelTestFinalClientResponseMatrix(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	service.InitHttpClient()
	originalStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() {
		constant.StreamingTimeout = originalStreamingTimeout
	})
	testUser := &model.User{
		Id:       1,
		Username: "channel-response-matrix-user",
		Password: "channel-response-matrix-password",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
		Quota:    1_000_000,
		Group:    "default",
	}
	testUser.SetSetting(dto.UserSetting{AcceptUnsetRatioModel: true})
	require.NoError(t, db.Create(testUser).Error)

	originalLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		common.LogConsumeEnabled = originalLogConsumeEnabled
	})

	chatResponse := `{"id":"chatcmpl-matrix","object":"chat.completion","created":1,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"reason"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`
	chatStream := strings.Join([]string{
		`data: {"id":"chatcmpl-matrix","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"reason"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-matrix","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-matrix","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"chatcmpl-matrix","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`,
		"data: [DONE]",
		"",
	}, "\n")
	responsesResponse := `{"id":"resp-matrix","object":"response","status":"completed","model":"test-model","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"reason"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}`
	responsesStream := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"reason"}`,
		`data: {"type":"response.output_text.delta","delta":"answer"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}}`,
		"",
	}, "\n")

	tests := []struct {
		name             string
		endpointType     constant.EndpointType
		stream           bool
		upstreamBody     string
		contentType      string
		wantContent      string
		wantReasoning    string
		wantRawSubstring string
	}{
		{name: "openai chat non-stream", endpointType: constant.EndpointTypeOpenAI, upstreamBody: chatResponse, contentType: "application/json", wantContent: "answer", wantReasoning: "reason", wantRawSubstring: "chatcmpl-matrix"},
		{name: "openai chat stream", endpointType: constant.EndpointTypeOpenAI, stream: true, upstreamBody: chatStream, contentType: "text/event-stream", wantContent: "answer", wantReasoning: "reason", wantRawSubstring: "chat.completion.chunk"},
		{name: "openai responses non-stream", endpointType: constant.EndpointTypeOpenAIResponse, upstreamBody: responsesResponse, contentType: "application/json", wantContent: "answer", wantReasoning: "reason", wantRawSubstring: "resp-matrix"},
		{name: "openai responses stream", endpointType: constant.EndpointTypeOpenAIResponse, stream: true, upstreamBody: responsesStream, contentType: "text/event-stream", wantContent: "answer", wantReasoning: "reason", wantRawSubstring: "response.output_text.delta"},
		{name: "anthropic non-stream", endpointType: constant.EndpointTypeAnthropic, upstreamBody: chatResponse, contentType: "application/json", wantContent: "answer", wantReasoning: "reason", wantRawSubstring: `"type":"message"`},
		{name: "anthropic stream", endpointType: constant.EndpointTypeAnthropic, stream: true, upstreamBody: chatStream, contentType: "text/event-stream", wantContent: "answer", wantReasoning: "reason", wantRawSubstring: "content_block_delta"},
		{name: "gemini non-stream", endpointType: constant.EndpointTypeGemini, upstreamBody: chatResponse, contentType: "application/json", wantContent: "answer", wantRawSubstring: "candidates"},
		{name: "gemini stream", endpointType: constant.EndpointTypeGemini, stream: true, upstreamBody: chatStream, contentType: "text/event-stream", wantContent: "answer", wantRawSubstring: "candidates"},
		{name: "responses compact", endpointType: constant.EndpointTypeOpenAIResponseCompact, upstreamBody: `{"id":"cmp-matrix","object":"response.compaction","created_at":1,"output":[{"type":"message","content":[{"type":"output_text","text":"compacted"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`, contentType: "application/json", wantRawSubstring: "cmp-matrix"},
		{name: "rerank", endpointType: constant.EndpointTypeJinaRerank, upstreamBody: `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":3}}`, contentType: "application/json", wantRawSubstring: "relevance_score"},
		{name: "image", endpointType: constant.EndpointTypeImageGeneration, upstreamBody: `{"created":1,"data":[{"b64_json":"c2VjcmV0"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`, contentType: "application/json", wantRawSubstring: "[binary data omitted]"},
		{name: "embeddings", endpointType: constant.EndpointTypeEmbeddings, upstreamBody: `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"test-model","usage":{"prompt_tokens":2,"total_tokens":2}}`, contentType: "application/json", wantRawSubstring: "embedding"},
	}

	paramOverride := common.GetJsonString(channelTestBodyResponseOverride())
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestCount := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requestCount++
				w.Header().Set("Content-Type", test.contentType)
				_, _ = io.WriteString(w, test.upstreamBody)
			}))
			defer upstream.Close()

			baseURL := upstream.URL
			channel := &model.Channel{
				Id:            200 + index,
				Type:          constant.ChannelTypeOpenAI,
				Key:           "test-key",
				Status:        common.ChannelStatusEnabled,
				Name:          "response matrix channel",
				BaseURL:       &baseURL,
				Models:        "test-model",
				Group:         "default",
				ParamOverride: &paramOverride,
				CreatedTime:   common.GetTimestamp(),
			}

			result := testChannel(t.Context(), channel, 1, channelTestOptions{
				model:        "test-model",
				endpointType: string(test.endpointType),
				isStream:     test.stream,
				userPrompt:   "hi",
			})

			require.NoError(t, result.localErr)
			require.Nil(t, result.newAPIError)
			assert.Equal(t, 1, requestCount)
			assert.NotEmpty(t, result.responseBody)
			assert.Equal(t, test.endpointType, result.effectiveEndpointType)
			assert.Equal(t, test.stream, result.isStream)
			assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(result.context))

			details := buildChannelTestResponseDetails(result)
			if channelTestEndpointSupportsStream(test.endpointType) {
				assert.Equal(t, test.wantContent, details.Content)
				assert.Equal(t, test.wantReasoning, details.ReasoningContent)
			}
			assert.Contains(t, details.RawResponse, test.wantRawSubstring)
			if test.endpointType == constant.EndpointTypeImageGeneration {
				assert.NotContains(t, details.RawResponse, "c2VjcmV0")
			}
		})
	}
}

// TestExtractChannelTestResponseContent verifies final answers and explicit reasoning stay separated.
func TestExtractChannelTestResponseContent(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantContent   string
		wantReasoning string
	}{
		{
			name:          "openai chat reasoning content",
			body:          `{"choices":[{"message":{"content":"final answer","reasoning_content":"explicit reasoning"}}]}`,
			wantContent:   "final answer",
			wantReasoning: "explicit reasoning",
		},
		{
			name:          "openai chat reasoning compatibility field",
			body:          `{"choices":[{"message":{"content":"final answer","reasoning":"compatibility reasoning"}}]}`,
			wantContent:   "final answer",
			wantReasoning: "compatibility reasoning",
		},
		{
			name:        "openai content parts preserve order",
			body:        `{"choices":[{"message":{"content":[{"type":"text","text":"hello"},{"type":"text","text":"world"}]}}]}`,
			wantContent: "hello\nworld",
		},
		{
			name:          "responses output and reasoning summary",
			body:          `{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"step one"},{"type":"summary_text","text":"step two"}]},{"type":"message","content":[{"type":"output_text","text":"hello"},{"type":"output_text","text":"world"}]}]}`,
			wantContent:   "hello\nworld",
			wantReasoning: "step one\nstep two",
		},
		{
			name:          "responses direct reasoning summary and text",
			body:          `{"output":[{"type":"reasoning","summary_text":"summary","content":[{"type":"reasoning_text","text":"reasoning text"}]}]}`,
			wantReasoning: "summary\nreasoning text",
		},
		{
			name:          "anthropic text and thinking blocks",
			body:          `{"content":[{"type":"thinking","thinking":"considering"},{"type":"text","text":"answer"}]}`,
			wantContent:   "answer",
			wantReasoning: "considering",
		},
		{
			name:          "gemini thought marker",
			body:          `{"candidates":[{"content":{"parts":[{"text":"analysis","thought":true},{"text":"answer"}]}}]}`,
			wantContent:   "answer",
			wantReasoning: "analysis",
		},
		{name: "reasoning only", body: `{"choices":[{"message":{"reasoning_content":"reasoning"}}]}`, wantReasoning: "reasoning"},
		{name: "answer only", body: `{"choices":[{"message":{"content":"answer"}}]}`, wantContent: "answer"},
		{name: "empty", body: `{}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content, reasoning := extractChannelTestResponseContent([]byte(test.body), false)
			assert.Equal(t, test.wantContent, content)
			assert.Equal(t, test.wantReasoning, reasoning)
		})
	}
}

// TestExtractChannelTestResponseTextKeepsPromptEffectContract verifies prompt-effect tests still read final answers only.
func TestExtractChannelTestResponseTextKeepsPromptEffectContract(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"final answer","reasoning_content":"explicit reasoning"}}]}`)

	assert.Equal(t, "final answer", extractChannelTestResponseText(body))
}

// TestExtractChannelTestStreamContent verifies supported SSE protocols aggregate deltas without duplicating terminal content.
func TestExtractChannelTestStreamContent(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantContent   string
		wantReasoning string
	}{
		{
			name: "openai chat deltas preserve whitespace",
			body: strings.Join([]string{
				`data: {"choices":[{"delta":{"reasoning_content":"think"}}]}`,
				`data: {"choices":[{"delta":{"content":"hello"}}]}`,
				`data: {"choices":[{"delta":{"content":" "}}]}`,
				`data: {"choices":[{"delta":{"content":"world"}}]}`,
				"data: [DONE]",
			}, "\n"),
			wantContent:   "hello world",
			wantReasoning: "think",
		},
		{
			name: "responses deltas ignore repeated terminal response",
			body: strings.Join([]string{
				`data: {"type":"response.reasoning_summary_text.delta","delta":"step"}`,
				`data: {"type":"response.output_text.delta","delta":"answer"}`,
				`data: {"type":"response.completed","response":{"output_text":"answer","output":[{"type":"reasoning","summary":[{"text":"step"}]}]}}`,
			}, "\n"),
			wantContent:   "answer",
			wantReasoning: "step",
		},
		{
			name: "anthropic text and thinking deltas",
			body: strings.Join([]string{
				`data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"reason"}}`,
				`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"answer"}}`,
			}, "\n"),
			wantContent:   "answer",
			wantReasoning: "reason",
		},
		{
			name: "gemini streaming parts",
			body: strings.Join([]string{
				`data: {"candidates":[{"content":{"parts":[{"text":"reason","thought":true}]}}]}`,
				`data: {"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`,
			}, "\n"),
			wantContent:   "answer",
			wantReasoning: "reason",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content, reasoning := extractChannelTestResponseContent([]byte(test.body), true)
			assert.Equal(t, test.wantContent, content)
			assert.Equal(t, test.wantReasoning, reasoning)
		})
	}
}

// TestBuildChannelTestResponseDetailsSanitizesBinaryData verifies display payloads do not expose encoded media.
func TestBuildChannelTestResponseDetailsSanitizesBinaryData(t *testing.T) {
	body := []byte(`{"choices":[{"message":{"content":"answer"}}],"image_url":{"url":"data:image/png;base64,aGVsbG8="},"source":{"type":"base64","data":"c2VjcmV0"}}`)

	details := buildChannelTestResponseDetails(testResult{
		responseBody:          body,
		effectiveEndpointType: constant.EndpointTypeOpenAI,
	})

	assert.Equal(t, constant.EndpointTypeOpenAI, details.EffectiveEndpointType)
	assert.False(t, details.Stream)
	assert.Equal(t, "answer", details.Content)
	assert.Contains(t, details.RawResponse, "[binary data omitted]")
	assert.NotContains(t, details.RawResponse, "aGVsbG8=")
	assert.NotContains(t, details.RawResponse, "c2VjcmV0")
	assert.False(t, details.RawResponseTruncated)
}

// TestBuildChannelTestResponseDetailsTruncatesAtUTF8Boundary verifies the 64 KiB display cap remains valid UTF-8.
func TestBuildChannelTestResponseDetailsTruncatesAtUTF8Boundary(t *testing.T) {
	body := []byte(strings.Repeat("界", maxChannelTestResponseDetailBytes))

	details := buildChannelTestResponseDetails(testResult{responseBody: body})

	assert.True(t, details.RawResponseTruncated)
	assert.LessOrEqual(t, len([]byte(details.RawResponse)), maxChannelTestResponseDetailBytes)
	assert.True(t, utf8.ValidString(details.RawResponse))
}

// TestBuildChannelTestResponseDetailsKeepsStreamCaptureFlag verifies capture truncation reaches the response contract.
func TestBuildChannelTestResponseDetailsKeepsStreamCaptureFlag(t *testing.T) {
	details := buildChannelTestResponseDetails(testResult{
		responseBody:          []byte("data: [DONE]\n\n"),
		responseBodyTruncated: true,
		isStream:              true,
	})

	assert.True(t, details.Stream)
	assert.True(t, details.RawResponseTruncated)
}

// TestValidateTestResponseBodyRejectsEmptyNonStreamBody protects successful tests from returning empty details.
func TestValidateTestResponseBodyRejectsEmptyNonStreamBody(t *testing.T) {
	err := validateTestResponseBody([]byte(" \n\t"), false)

	require.Error(t, err)
	assert.Equal(t, "response body is empty", err.Error())
}

// TestChannelTestResponseOverrideFinalizationMatrix verifies every buffered test protocol commits, rejects, and fails open identically.
func TestChannelTestResponseOverrideFinalizationMatrix(t *testing.T) {
	protocols := []struct {
		name        string
		relayFormat types.RelayFormat
		relayMode   int
		requestPath string
	}{
		{name: "openai chat", relayFormat: types.RelayFormatOpenAI, relayMode: relayconstant.RelayModeChatCompletions, requestPath: "/v1/chat/completions"},
		{name: "openai responses", relayFormat: types.RelayFormatOpenAIResponses, relayMode: relayconstant.RelayModeResponses, requestPath: "/v1/responses"},
		{name: "anthropic", relayFormat: types.RelayFormatClaude, relayMode: relayconstant.RelayModeChatCompletions, requestPath: "/v1/messages"},
		{name: "gemini", relayFormat: types.RelayFormatGemini, relayMode: relayconstant.RelayModeGemini, requestPath: "/v1beta/models/test:generateContent"},
		{name: "responses compact", relayFormat: types.RelayFormatOpenAIResponsesCompaction, relayMode: relayconstant.RelayModeResponsesCompact, requestPath: "/v1/responses/compact"},
	}

	for _, protocol := range protocols {
		t.Run(protocol.name, func(t *testing.T) {
			tests := []struct {
				name            string
				override        map[string]interface{}
				body            string
				wantClientErr   bool
				wantReason      string
				wantBodyVisible bool
			}{
				{
					name:            "no match commits candidate",
					override:        channelTestBodyResponseOverride(),
					body:            `{"blocked":false,"content":"visible"}`,
					wantReason:      relaycommon.ResponseOverrideNotAppliedNoMatch,
					wantBodyVisible: true,
				},
				{
					name:            "match discards candidate",
					override:        channelTestBodyResponseOverride(),
					body:            `{"blocked":true,"content":"hidden"}`,
					wantClientErr:   true,
					wantBodyVisible: false,
				},
				{
					name: "configuration error fails open",
					override: map[string]interface{}{
						"operations": []interface{}{
							map[string]interface{}{
								"phase":      "response",
								"mode":       "return_error",
								"value":      map[string]interface{}{"message": "invalid rule"},
								"conditions": "invalid",
							},
						},
					},
					body:            `{"content":"visible after fail open"}`,
					wantReason:      relaycommon.ResponseOverrideNotAppliedConfigError,
					wantBodyVisible: true,
				},
			}

			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(recorder)
					ctx.Request = httptest.NewRequest(http.MethodPost, protocol.requestPath, nil)
					info := &relaycommon.RelayInfo{
						RelayFormat:    protocol.relayFormat,
						RelayMode:      protocol.relayMode,
						RequestURLPath: protocol.requestPath,
						ChannelMeta: &relaycommon.ChannelMeta{
							ParamOverride: test.override,
						},
					}
					relaycommon.StartResponseOverrideBuffer(ctx, info)
					require.NotNil(t, relaycommon.CurrentResponseOverrideBuffer(ctx))
					ctx.Writer.WriteHeader(http.StatusOK)
					_, err := ctx.Writer.Write([]byte(test.body))
					require.NoError(t, err)

					decision := service.EvaluateResponseOverrideBeforeSettlement(ctx, info, &dto.Usage{TotalTokens: 4}, http.StatusOK)
					require.NotNil(t, decision)
					clientErr := finalizeResponseOverride(ctx, info)

					assert.Equal(t, test.wantClientErr, clientErr != nil)
					assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(ctx))
					if test.wantReason != "" {
						assert.Equal(t, test.wantReason, decision.NotAppliedReason)
					}
					if test.wantBodyVisible {
						assert.JSONEq(t, test.body, recorder.Body.String())
					} else {
						assert.Empty(t, recorder.Body.String())
					}
				})
			}
		})
	}
}

// TestChannelTestStreamingOverrideBypassMatrix verifies all streaming text protocols write SSE directly and remain extractable.
func TestChannelTestStreamingOverrideBypassMatrix(t *testing.T) {
	tests := []struct {
		name          string
		relayFormat   types.RelayFormat
		relayMode     int
		requestPath   string
		endpointType  constant.EndpointType
		body          string
		wantContent   string
		wantReasoning string
	}{
		{name: "openai chat", relayFormat: types.RelayFormatOpenAI, relayMode: relayconstant.RelayModeChatCompletions, requestPath: "/v1/chat/completions", endpointType: constant.EndpointTypeOpenAI, body: "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"reason\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n", wantContent: "answer", wantReasoning: "reason"},
		{name: "openai responses", relayFormat: types.RelayFormatOpenAIResponses, relayMode: relayconstant.RelayModeResponses, requestPath: "/v1/responses", endpointType: constant.EndpointTypeOpenAIResponse, body: "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"reason\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n", wantContent: "answer", wantReasoning: "reason"},
		{name: "anthropic", relayFormat: types.RelayFormatClaude, relayMode: relayconstant.RelayModeChatCompletions, requestPath: "/v1/messages", endpointType: constant.EndpointTypeAnthropic, body: "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"reason\"}}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n", wantContent: "answer", wantReasoning: "reason"},
		{name: "gemini", relayFormat: types.RelayFormatGemini, relayMode: relayconstant.RelayModeGemini, requestPath: "/v1beta/models/test:streamGenerateContent?alt=sse", endpointType: constant.EndpointTypeGemini, body: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"reason\",\"thought\":true}]}}]}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}]}}]}\n\n", wantContent: "answer", wantReasoning: "reason"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, test.requestPath, nil)
			info := &relaycommon.RelayInfo{
				IsStream:       true,
				RelayFormat:    test.relayFormat,
				RelayMode:      test.relayMode,
				RequestURLPath: test.requestPath,
				ChannelMeta: &relaycommon.ChannelMeta{
					ParamOverride: channelTestBodyResponseOverride(),
				},
			}
			relaycommon.StartResponseOverrideBuffer(ctx, info)
			assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(ctx))
			require.NotNil(t, info.ResponseOverride)
			assert.Equal(t, relaycommon.ResponseOverrideNotAppliedStreaming, info.ResponseOverride.NotAppliedReason)

			_, err := ctx.Writer.Write([]byte(test.body))
			require.NoError(t, err)
			assert.Nil(t, finalizeResponseOverride(ctx, info))
			assert.Equal(t, test.body, recorder.Body.String())

			details := buildChannelTestResponseDetails(testResult{
				responseBody:          recorder.Body.Bytes(),
				effectiveEndpointType: test.endpointType,
				isStream:              true,
			})
			assert.Equal(t, test.wantContent, details.Content)
			assert.Equal(t, test.wantReasoning, details.ReasoningContent)
		})
	}
}

// TestChannelTestStructuredEndpointsBypassResponseBuffer verifies non-text JSON remains directly visible.
func TestChannelTestStructuredEndpointsBypassResponseBuffer(t *testing.T) {
	tests := []struct {
		name         string
		relayFormat  types.RelayFormat
		relayMode    int
		endpointType constant.EndpointType
		requestPath  string
		body         string
	}{
		{name: "embeddings", relayFormat: types.RelayFormatEmbedding, relayMode: relayconstant.RelayModeEmbeddings, endpointType: constant.EndpointTypeEmbeddings, requestPath: "/v1/embeddings", body: `{"data":[{"embedding":[0.1,0.2]}],"usage":{"total_tokens":2}}`},
		{name: "image", relayFormat: types.RelayFormatOpenAIImage, relayMode: relayconstant.RelayModeImagesGenerations, endpointType: constant.EndpointTypeImageGeneration, requestPath: "/v1/images/generations", body: `{"data":[{"b64_json":"c2VjcmV0"}]}`},
		{name: "rerank", relayFormat: types.RelayFormatRerank, relayMode: relayconstant.RelayModeRerank, endpointType: constant.EndpointTypeJinaRerank, requestPath: "/v1/rerank", body: `{"results":[{"index":0,"relevance_score":0.9}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, test.requestPath, nil)
			info := &relaycommon.RelayInfo{
				RelayFormat:    test.relayFormat,
				RelayMode:      test.relayMode,
				RequestURLPath: test.requestPath,
				ChannelMeta: &relaycommon.ChannelMeta{
					ParamOverride: channelTestBodyResponseOverride(),
				},
			}
			relaycommon.StartResponseOverrideBuffer(ctx, info)
			assert.Nil(t, relaycommon.CurrentResponseOverrideBuffer(ctx))

			_, err := ctx.Writer.Write([]byte(test.body))
			require.NoError(t, err)
			details := buildChannelTestResponseDetails(testResult{
				responseBody:          recorder.Body.Bytes(),
				effectiveEndpointType: test.endpointType,
			})
			assert.Empty(t, details.Content)
			assert.Empty(t, details.ReasoningContent)
			assert.NotEmpty(t, details.RawResponse)
			if test.endpointType == constant.EndpointTypeImageGeneration {
				assert.Contains(t, details.RawResponse, "[binary data omitted]")
				assert.NotContains(t, details.RawResponse, "c2VjcmV0")
			}
		})
	}
}

// channelTestBodyResponseOverride builds a deterministic response-body rule for controller lifecycle tests.
func channelTestBodyResponseOverride() map[string]interface{} {
	return map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"phase": "response",
				"mode":  "return_error",
				"value": map[string]interface{}{
					"message":     "blocked by test rule",
					"status_code": http.StatusUnprocessableEntity,
				},
				"conditions": []interface{}{
					map[string]interface{}{
						"source": "body",
						"path":   "blocked",
						"mode":   "full",
						"value":  true,
					},
				},
			},
		},
	}
}

// TestReadTestResponseBodyCapsStreamCapture verifies streaming memory stays bounded while non-stream responses remain available for extraction.
func TestReadTestResponseBodyCapsStreamCapture(t *testing.T) {
	body := strings.Repeat("a", maxChannelTestResponseDetailBytes+128)

	streamBody, streamTruncated, err := readTestResponseBody(io.NopCloser(strings.NewReader(body)), true)
	require.NoError(t, err)
	assert.Len(t, streamBody, maxChannelTestResponseDetailBytes)
	assert.True(t, streamTruncated)

	nonStreamBody, nonStreamTruncated, err := readTestResponseBody(io.NopCloser(strings.NewReader(body)), false)
	require.NoError(t, err)
	assert.Len(t, nonStreamBody, len(body))
	assert.False(t, nonStreamTruncated)
}

// TestReadTestResponseBodyPreservesStreamUTF8 verifies capture truncation never returns a split code point.
func TestReadTestResponseBodyPreservesStreamUTF8(t *testing.T) {
	prefix := strings.Repeat("a", maxChannelTestResponseDetailBytes-1)
	body := prefix + "界" + strings.Repeat("b", 16)

	streamBody, streamTruncated, err := readTestResponseBody(
		io.NopCloser(strings.NewReader(body)),
		true,
	)

	require.NoError(t, err)
	assert.True(t, streamTruncated)
	assert.True(t, utf8.Valid(streamBody))
	assert.LessOrEqual(t, len(streamBody), maxChannelTestResponseDetailBytes)
}

func TestSettleTestQuotaUsesTieredBilling(t *testing.T) {
	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:   "tiered_expr",
			ExprString:    `param("stream") == true ? tier("stream", p * 3) : tier("base", p * 2)`,
			ExprHash:      billingexpr.ExprHashString(`param("stream") == true ? tier("stream", p * 3) : tier("base", p * 2)`),
			GroupRatio:    1,
			EstimatedTier: "stream",
			QuotaPerUnit:  common.QuotaPerUnit,
			ExprVersion:   1,
		},
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{"stream":true}`),
		},
	}

	quota, result := settleTestQuota(info, types.PriceData{
		ModelRatio:      1,
		CompletionRatio: 2,
	}, &dto.Usage{
		PromptTokens: 1000,
	})

	require.Equal(t, 1500, quota)
	require.NotNil(t, result)
	require.Equal(t, "stream", result.MatchedTier)
}

func TestBuildTestLogOtherInjectsTieredInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	info := &relaycommon.RelayInfo{
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr",
			ExprString:  `tier("base", p * 2)`,
		},
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	priceData := types.PriceData{
		GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
	}
	usage := &dto.Usage{
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 12,
		},
	}

	other := buildTestLogOther(ctx, info, priceData, usage, &billingexpr.TieredResult{
		MatchedTier: "base",
	})

	require.Equal(t, "tiered_expr", other["billing_mode"])
	require.Equal(t, "base", other["matched_tier"])
	require.NotEmpty(t, other["expr_b64"])
}

func TestResolveChannelTestUserIDUsesRequestUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("id", 2)

	userID, err := resolveChannelTestUserID(ctx)

	require.NoError(t, err)
	require.Equal(t, 2, userID)
}

func TestClassifyNativeProbeResult(t *testing.T) {
	tests := []struct {
		name   string
		result testResult
		want   string
	}{
		{name: "confirmed", result: testResult{upstreamStatus: http.StatusOK}, want: "confirmed"},
		{name: "path not found", result: testResult{localErr: assert.AnError, upstreamStatus: http.StatusNotFound}, want: "path_mismatch"},
		{name: "method not allowed", result: testResult{localErr: assert.AnError, upstreamStatus: http.StatusMethodNotAllowed}, want: "path_mismatch"},
		{name: "authentication", result: testResult{localErr: assert.AnError, upstreamStatus: http.StatusUnauthorized}, want: "auth_error"},
		{name: "rate limited", result: testResult{localErr: assert.AnError, upstreamStatus: http.StatusTooManyRequests}, want: "rate_limited"},
		{name: "upstream format", result: testResult{localErr: assert.AnError, upstreamStatus: http.StatusOK}, want: "upstream_error"},
		{name: "upstream service", result: testResult{localErr: assert.AnError, upstreamStatus: http.StatusBadGateway}, want: "upstream_error"},
		{name: "transport", result: testResult{localErr: assert.AnError}, want: "transport_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, classifyNativeProbeResult(test.result))
		})
	}
}

func TestClassifyMultiKeyTestResult(t *testing.T) {
	tests := []struct {
		name   string
		result testResult
		want   string
	}{
		{name: "available", result: testResult{upstreamStatus: http.StatusOK}, want: "available"},
		{name: "authentication status fallback", result: testResult{newAPIError: types.NewErrorWithStatusCode(assert.AnError, types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized), upstreamStatus: http.StatusUnauthorized}, want: "auth_failed"},
		{name: "forbidden status fallback", result: testResult{newAPIError: types.NewErrorWithStatusCode(assert.AnError, types.ErrorCodeBadResponseStatusCode, http.StatusForbidden), upstreamStatus: http.StatusForbidden}, want: "auth_failed"},
		{name: "quota status fallback", result: testResult{newAPIError: types.NewErrorWithStatusCode(assert.AnError, types.ErrorCodeBadResponseStatusCode, http.StatusPaymentRequired), upstreamStatus: http.StatusPaymentRequired}, want: "quota_exhausted"},
		{name: "usage limit overrides generic response error", result: testResult{newAPIError: types.NewErrorWithStatusCode(errors.New("You've reached your usage limit for this billing cycle. Your quota will be refreshed in the next cycle."), types.ErrorCodeBadResponse, http.StatusForbidden), upstreamStatus: http.StatusForbidden}, want: "quota_exhausted"},
		{name: "insufficient credits override forbidden status", result: testResult{newAPIError: types.NewErrorWithStatusCode(errors.New("Insufficient credits for this request"), types.ErrorCode("unknown_error"), http.StatusForbidden), upstreamStatus: http.StatusForbidden}, want: "quota_exhausted"},
		{name: "membership verification overrides payment status", result: testResult{newAPIError: types.NewErrorWithStatusCode(errors.New("We're unable to verify your membership benefits at this time. Please ensure your membership is active."), types.ErrorCode("unknown_error"), http.StatusPaymentRequired), upstreamStatus: http.StatusPaymentRequired}, want: "auth_failed"},
		{name: "invalid key overrides payment status", result: testResult{newAPIError: types.NewErrorWithStatusCode(errors.New("Invalid API key"), types.ErrorCode("unknown_error"), http.StatusPaymentRequired), upstreamStatus: http.StatusPaymentRequired}, want: "auth_failed"},
		{name: "rate limited", result: testResult{newAPIError: types.NewErrorWithStatusCode(assert.AnError, types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), upstreamStatus: http.StatusTooManyRequests}, want: "rate_limited"},
		{name: "model forbidden", result: testResult{newAPIError: types.NewError(assert.AnError, types.ErrorCodeModelNotFound)}, want: "model_forbidden"},
		{name: "configuration", result: testResult{newAPIError: types.NewError(assert.AnError, types.ErrorCodeGetChannelFailed)}, want: "configuration_error"},
		{name: "response", result: testResult{newAPIError: types.NewError(assert.AnError, types.ErrorCodeBadResponseBody), upstreamStatus: http.StatusOK}, want: "response_error"},
		{name: "upstream", result: testResult{newAPIError: types.NewErrorWithStatusCode(assert.AnError, types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), upstreamStatus: http.StatusBadGateway}, want: "upstream_error"},
		{name: "request failed", result: testResult{newAPIError: types.NewError(assert.AnError, types.ErrorCodeDoRequestFailed)}, want: "network_error"},
		{name: "network", result: testResult{localErr: assert.AnError}, want: "network_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, classifyMultiKeyTestResult(test.result))
		})
	}
}

func TestRespondMultiKeyConnectionTestMasksSensitiveError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	testContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(testContext, constant.ContextKeyChannelKey, "sk-secret-value")
	apiErr := types.NewErrorWithStatusCode(
		errors.New("upstream rejected sk-secret-value"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusUnauthorized,
	)

	respondMultiKeyConnectionTest(c, time.Now(), testResult{
		context:        testContext,
		newAPIError:    apiErr,
		upstreamStatus: http.StatusUnauthorized,
	}, 2)

	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, false, response["success"])
	assert.Equal(t, float64(2), response["key_index"])
	assert.Equal(t, "auth_failed", response["classification"])
	assert.NotContains(t, response["message"], "sk-secret-value")
}

func TestSelectChannelsForAutomaticTestPassiveRecoveryOnlyUsesAutoDisabled(t *testing.T) {
	channels := []*model.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Status: common.ChannelStatusAutoDisabled},
		{Id: 3, Status: common.ChannelStatusManuallyDisabled},
	}

	selected := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModePassiveRecovery)

	require.Len(t, selected, 1)
	require.Equal(t, 2, selected[0].Id)
}

func TestSelectChannelsForAutomaticTestScheduledSkipsManualDisabled(t *testing.T) {
	channels := []*model.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled},
		{Id: 2, Status: common.ChannelStatusAutoDisabled},
		{Id: 3, Status: common.ChannelStatusManuallyDisabled},
	}

	selected := selectChannelsForAutomaticTest(channels, operation_setting.ChannelTestModeScheduledAll)

	require.Len(t, selected, 2)
	require.Equal(t, 1, selected[0].Id)
	require.Equal(t, 2, selected[1].Id)
}

func TestTestAllChannelsRejectsExistingActiveTask(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))

	existing, err := model.CreateSystemTask(model.SystemTaskTypeChannelTest, nil, nil)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/channel/test", nil)

	TestAllChannels(ctx)

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), existing.TaskID)
	require.Contains(t, recorder.Body.String(), "已有通道测试任务正在运行或等待中")
}
