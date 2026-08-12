package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
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

	details := buildChannelTestResponseDetails(testResult{responseBody: body}, false)

	assert.Equal(t, "answer", details.Content)
	assert.Contains(t, details.RawResponse, "[binary data omitted]")
	assert.NotContains(t, details.RawResponse, "aGVsbG8=")
	assert.NotContains(t, details.RawResponse, "c2VjcmV0")
	assert.False(t, details.RawResponseTruncated)
}

// TestBuildChannelTestResponseDetailsTruncatesAtUTF8Boundary verifies the 64 KiB display cap remains valid UTF-8.
func TestBuildChannelTestResponseDetailsTruncatesAtUTF8Boundary(t *testing.T) {
	body := []byte(strings.Repeat("界", maxChannelTestResponseDetailBytes))

	details := buildChannelTestResponseDetails(testResult{responseBody: body}, false)

	assert.True(t, details.RawResponseTruncated)
	assert.LessOrEqual(t, len([]byte(details.RawResponse)), maxChannelTestResponseDetailBytes)
	assert.True(t, utf8.ValidString(details.RawResponse))
}

// TestBuildChannelTestResponseDetailsKeepsStreamCaptureFlag verifies capture truncation reaches the response contract.
func TestBuildChannelTestResponseDetailsKeepsStreamCaptureFlag(t *testing.T) {
	details := buildChannelTestResponseDetails(testResult{
		responseBody:          []byte("data: [DONE]\n\n"),
		responseBodyTruncated: true,
	}, true)

	assert.True(t, details.RawResponseTruncated)
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
