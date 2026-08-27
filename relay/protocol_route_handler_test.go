package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/service/relaynormalize"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func convertedResponsesViaChatTestInfo(baseURL string, stream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		IsStream:           stream,
		DisablePing:        true,
		RelayMode:          relayconstant.RelayModeResponses,
		RelayFormat:        types.RelayFormatOpenAIResponses,
		RequestURLPath:     "/v1/responses",
		RequestedModelName: "MODEL_X",
		RoutingModelName:   "MODEL_X",
		AttemptModelName:   "MODEL_X",
		OriginModelName:    "MODEL_X",
		StartTime:          time.Now(),
		RequestConversionChain: []types.RelayFormat{
			types.RelayFormatOpenAIResponses,
		},
		ChannelRoutePlan: &types.ChannelRoutePlan{
			ClientEndpointType:   constant.EndpointTypeOpenAIResponse,
			UpstreamEndpointType: constant.EndpointTypeOpenAI,
			ClientRelayFormat:    types.RelayFormatOpenAIResponses,
			UpstreamRelayFormat:  types.RelayFormatOpenAI,
			ClientRelayMode:      relayconstant.RelayModeResponses,
			UpstreamRelayMode:    relayconstant.RelayModeChatCompletions,
			ClientPath:           "/v1/responses",
			UpstreamPath:         "/v1/chat/completions",
			RouteMode:            types.ChannelRouteModeConverted,
			RequestConverter:     relayconvert.ConverterOpenAIResponsesToOpenAIChat,
			ResponseConverter:    relayconvert.ConverterOpenAIChatToOpenAIResponses,
			Quality:              "good",
			RequestSteps:         1,
			ResponseSteps:        1,
			Stream:               stream,
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelId:         1,
			ChannelBaseUrl:    baseURL,
			ApiType:           constant.APITypeOpenAI,
			ApiKey:            "TOKEN",
			UpstreamModelName: "MODEL_X",
		},
	}
}

func TestExecuteConvertedTextRouteResponsesViaChatJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer TOKEN", r.Header.Get("Authorization"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request dto.GeneralOpenAIRequest
		require.NoError(t, common.Unmarshal(body, &request))
		assert.Equal(t, "MODEL_X", request.Model)
		assert.Len(t, request.Messages, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"MODEL_X","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := convertedResponsesViaChatTestInfo(upstream.URL, false)
	request := &dto.OpenAIResponsesRequest{
		Model: "MODEL_X",
		Input: json.RawMessage(`[{"role":"user","content":"hello"}]`),
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)

	usage, apiError := executeConvertedTextRoute(c, info, adaptor, request)
	require.Nil(t, apiError)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Equal(t, http.StatusOK, recorder.Code)
	var response dto.OpenAIResponsesResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "MODEL_X", response.Model)
	assert.NotEmpty(t, response.Output)
	assert.Equal(t, types.RelayFormatOpenAI, info.GetFinalRequestRelayFormat())
}

func TestExecuteConvertedTextRouteMessagesBetaPreservesReasoningToolHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request dto.GeneralOpenAIRequest
		require.NoError(t, common.Unmarshal(body, &request))
		require.Len(t, request.Messages, 3)
		assistant := request.Messages[1]
		assert.Equal(t, "reasoning history", assistant.GetReasoningContent())
		require.Len(t, assistant.ParseContent(), 1)
		assert.Equal(t, "visible", assistant.ParseContent()[0].Text)
		require.Len(t, assistant.ParseToolCalls(), 1)
		assert.Equal(t, "call_1", assistant.ParseToolCalls()[0].ID)
		assert.Equal(t, "tool", request.Messages[2].Role)
		assert.Equal(t, "call_1", request.Messages[2].ToolCallId)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_2","object":"chat.completion","created":1,"model":"MODEL_X","choices":[{"index":0,"message":{"role":"assistant","content":"next visible","reasoning_content":"next reasoning","tool_calls":[{"id":"call_2","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"y\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages?beta=true", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatClaude,
		RequestURLPath:     "/v1/messages?beta=true",
		IsClaudeBetaQuery:  true,
		RequestedModelName: "MODEL_X",
		RoutingModelName:   "MODEL_X",
		AttemptModelName:   "MODEL_X",
		OriginModelName:    "MODEL_X",
		StartTime:          time.Now(),
		RequestConversionChain: []types.RelayFormat{
			types.RelayFormatClaude,
		},
		ChannelRoutePlan: &types.ChannelRoutePlan{
			ClientEndpointType:   constant.EndpointTypeAnthropic,
			UpstreamEndpointType: constant.EndpointTypeOpenAI,
			ClientRelayFormat:    types.RelayFormatClaude,
			UpstreamRelayFormat:  types.RelayFormatOpenAI,
			UpstreamRelayMode:    relayconstant.RelayModeChatCompletions,
			ClientPath:           "/v1/messages?beta=true",
			UpstreamPath:         "/v1/chat/completions",
			RouteMode:            types.ChannelRouteModeConverted,
			RequestConverter:     relayconvert.ConverterClaudeMessagesToOpenAIChat,
			ResponseConverter:    relayconvert.ConverterOpenAIChatToClaudeMessages,
			Quality:              "fair",
			RequestSteps:         1,
			ResponseSteps:        1,
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelId:         42,
			ChannelBaseUrl:    upstream.URL,
			ApiType:           constant.APITypeOpenAI,
			ApiKey:            "TOKEN",
			UpstreamModelName: "MODEL_X",
		},
	}
	request := &dto.ClaudeRequest{
		Model: "MODEL_X",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{
				{Type: "thinking", Thinking: common.GetPointer("reasoning history"), Signature: common.GetPointer("")},
				{Type: "text", Text: common.GetPointer("visible")},
				{Type: "tool_use", Id: "call_1", Name: "lookup", Input: map[string]any{"q": "x"}},
			}},
			{Role: "user", Content: []dto.ClaudeMediaMessage{
				{Type: "tool_result", ToolUseId: "call_1", Content: "result"},
			}},
		},
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)

	usage, apiError := executeConvertedTextRoute(c, info, adaptor, request)
	require.Nil(t, apiError)
	require.NotNil(t, usage)
	assert.Equal(t, 10, usage.TotalTokens)
	assert.Equal(t, http.StatusOK, recorder.Code)

	var response dto.ClaudeResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Content, 3)
	assert.Equal(t, "thinking", response.Content[0].Type)
	assert.Equal(t, "next reasoning", *response.Content[0].Thinking)
	require.NotNil(t, response.Content[0].Signature)
	assert.Empty(t, *response.Content[0].Signature)
	assert.Equal(t, "text", response.Content[1].Type)
	assert.Equal(t, "tool_use", response.Content[2].Type)
	require.NotNil(t, info.ReasoningHistory)
	assert.Equal(t, 2, info.ReasoningHistory.PreservedMessages)
	assert.Equal(t, 1, info.ReasoningHistory.SyntheticClientSignatures)
}

func TestExecuteConvertedTextRouteResponsesViaChatStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() {
		constant.StreamingTimeout = oldStreamingTimeout
	}()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"MODEL_X\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"MODEL_X\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"MODEL_X\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := convertedResponsesViaChatTestInfo(upstream.URL, true)
	request := &dto.OpenAIResponsesRequest{
		Model:  "MODEL_X",
		Input:  json.RawMessage(`[{"role":"user","content":"hello"}]`),
		Stream: common.GetPointer(true),
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)

	usage, apiError := executeConvertedTextRoute(c, info, adaptor, request)
	require.Nil(t, apiError)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)
	body := recorder.Body.String()
	assert.True(t, strings.Contains(body, "event: response.created"), body)
	assert.True(t, strings.Contains(body, "event: response.completed"), body)
}

func TestExecuteConvertedTextRouteResponsesViaChatRejectsMissingDoneMarker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"MODEL_X\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := convertedResponsesViaChatTestInfo(upstream.URL, true)
	request := &dto.OpenAIResponsesRequest{
		Model:  "MODEL_X",
		Input:  json.RawMessage(`[{"role":"user","content":"hello"}]`),
		Stream: common.GetPointer(true),
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)

	usage, apiError := executeConvertedTextRoute(c, info, adaptor, request)

	assert.Nil(t, usage)
	require.NotNil(t, apiError)
	assert.Equal(t, types.ErrorCodeBadResponse, apiError.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiError))
	assert.Contains(t, recorder.Body.String(), "partial")
	assert.NotContains(t, recorder.Body.String(), "event: response.completed")
	reason, _ := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonUnexpectedEOF, reason)
	assert.True(t, info.StreamStatus.ClientPayloadIsCommitted())
	partialUsage, hasPartialUsage, estimated := info.StreamStatus.PartialUsageSnapshot()
	assert.True(t, hasPartialUsage)
	assert.True(t, estimated)
	assert.Positive(t, partialUsage.CompletionTokens)
}

func TestHandleNativeResponsesStreamUsesTypedTerminalWithoutDoneMarker(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	body := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		`data: {"type":"response.incomplete","response":{"status":"incomplete","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}}`,
		``,
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		DisablePing: true,
		RelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_X"},
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiError := HandleNativeTextResponse(c, info, resp, types.RelayFormatOpenAIResponses, true)

	require.Nil(t, apiError)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Contains(t, recorder.Body.String(), "event: response.incomplete")
	event, status := info.StreamStatus.Terminal()
	assert.Equal(t, "response.incomplete", event)
	assert.Equal(t, "incomplete", status)
}

func TestHandleNativeResponsesStreamRejectsUnexpectedEOF(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	body := `data: {"type":"response.output_text.delta","delta":"partial"}` + "\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		DisablePing: true,
		RelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_X"},
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiError := HandleNativeTextResponse(c, info, resp, types.RelayFormatOpenAIResponses, true)

	assert.Nil(t, usage)
	require.NotNil(t, apiError)
	assert.True(t, types.IsSkipRetryError(apiError))
	reason, _ := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonUnexpectedEOF, reason)
	assert.True(t, info.StreamStatus.ClientPayloadIsCommitted())
	partialUsage, hasPartialUsage, estimated := info.StreamStatus.PartialUsageSnapshot()
	assert.True(t, hasPartialUsage)
	assert.True(t, estimated)
	assert.Positive(t, partialUsage.CompletionTokens)
}

func TestHandleNativeResponsesStreamRecordsFailedTerminal(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	body := `data: {"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"stream_aborted","message":"mid stream aborted"}}}` + "\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		DisablePing: true,
		RelayFormat: types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_X"},
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiError := HandleNativeTextResponse(c, info, resp, types.RelayFormatOpenAIResponses, true)

	assert.Nil(t, usage)
	require.NotNil(t, apiError)
	assert.ErrorContains(t, apiError, "mid stream aborted")
	event, status := info.StreamStatus.Terminal()
	assert.Equal(t, "response.failed", event)
	assert.Equal(t, "failed", status)
	assert.False(t, info.StreamStatus.ClientPayloadIsCommitted())
}

func TestHandleNativeChatStreamRejectsEOFMissingTerminal(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"content":"first"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`,
		``,
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		DisablePing: true,
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_X"},
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiError := HandleNativeTextResponse(c, info, resp, types.RelayFormatOpenAI, true)

	assert.Nil(t, usage)
	require.NotNil(t, apiError)
	assert.True(t, types.IsSkipRetryError(apiError))
	assert.NotContains(t, recorder.Body.String(), "data: [DONE]")
	reason, endErr := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonUnexpectedEOF, reason)
	assert.EqualError(t, endErr, "stream ended before terminal event")
	assert.True(t, info.StreamStatus.ClientPayloadIsCommitted())
	partialUsage, hasPartialUsage, estimated := info.StreamStatus.PartialUsageSnapshot()
	assert.True(t, hasPartialUsage)
	assert.True(t, estimated)
	assert.Positive(t, partialUsage.CompletionTokens)
}

func TestHandleNativeChatStreamAcceptsFinishReasonWithoutDoneMarker(t *testing.T) {
	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldStreamingTimeout })

	body := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{"content":"complete"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"MODEL_X","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
		``,
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{
		IsStream:    true,
		DisablePing: true,
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MODEL_X"},
	}
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}

	usage, apiError := HandleNativeTextResponse(c, info, resp, types.RelayFormatOpenAI, true)

	require.Nil(t, apiError)
	require.NotNil(t, usage)
	assert.Equal(t, 3, usage.TotalTokens)
	assert.Contains(t, recorder.Body.String(), `"finish_reason":"stop"`)
	assert.Contains(t, recorder.Body.String(), "data: [DONE]")
	reason, endErr := info.StreamStatus.End()
	assert.Equal(t, relaycommon.StreamEndReasonDone, reason)
	assert.NoError(t, endErr)
	event, status := info.StreamStatus.Terminal()
	assert.Equal(t, "chat.finish_reason", event)
	assert.Equal(t, "completed", status)
}

func TestExecuteConvertedTextRoutePreservesEndpointMismatchBeforeStatusMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"route missing","type":"invalid_request_error"}}`)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("status_code_mapping", `{"404":503}`)
	info := convertedResponsesViaChatTestInfo(upstream.URL, false)
	request := &dto.OpenAIResponsesRequest{
		Model: "MODEL_X",
		Input: json.RawMessage(`[{"role":"user","content":"hello"}]`),
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)

	usage, apiError := executeConvertedTextRoute(c, info, adaptor, request)

	assert.Nil(t, usage)
	require.NotNil(t, apiError)
	assert.Equal(t, http.StatusServiceUnavailable, apiError.StatusCode)
	assert.True(t, info.ProtocolEndpointMismatch)
}

func TestExecuteNativeTextRoutePreservesEndpointMismatchBeforeStatusMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, `{"error":{"message":"method rejected","type":"invalid_request_error"}}`)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("status_code_mapping", `{"405":503}`)
	info := convertedResponsesViaChatTestInfo(upstream.URL, false)
	info.ChannelRoutePlan = &types.ChannelRoutePlan{
		ClientEndpointType:   constant.EndpointTypeOpenAIResponse,
		UpstreamEndpointType: constant.EndpointTypeOpenAIResponse,
		ClientRelayFormat:    types.RelayFormatOpenAIResponses,
		UpstreamRelayFormat:  types.RelayFormatOpenAIResponses,
		ClientRelayMode:      relayconstant.RelayModeResponses,
		UpstreamRelayMode:    relayconstant.RelayModeResponses,
		ClientPath:           "/v1/responses",
		UpstreamPath:         "/v1/responses",
		RouteMode:            types.ChannelRouteModeNative,
	}
	request := &dto.OpenAIResponsesRequest{
		Model: "MODEL_X",
		Input: json.RawMessage(`[{"role":"user","content":"hello"}]`),
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)

	usage, apiError := executeNativeTextRoute(c, info, adaptor, request, false)

	assert.Nil(t, usage)
	require.NotNil(t, apiError)
	assert.Equal(t, http.StatusServiceUnavailable, apiError.StatusCode)
	assert.True(t, info.ProtocolEndpointMismatch)
}

func TestExecuteNativeTextRouteMessagesKeepsPathAndWireFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request dto.ClaudeRequest
		require.NoError(t, common.Unmarshal(body, &request))
		assert.Equal(t, "MODEL_X", request.Model)
		require.Len(t, request.Messages, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"MODEL_X","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatClaude,
		RequestURLPath:     "/v1/messages",
		RequestedModelName: "MODEL_X",
		RoutingModelName:   "MODEL_X",
		AttemptModelName:   "MODEL_X",
		OriginModelName:    "MODEL_X",
		StartTime:          time.Now(),
		ChannelRoutePlan: &types.ChannelRoutePlan{
			ClientEndpointType:   constant.EndpointTypeAnthropic,
			UpstreamEndpointType: constant.EndpointTypeAnthropic,
			ClientRelayFormat:    types.RelayFormatClaude,
			UpstreamRelayFormat:  types.RelayFormatClaude,
			RouteMode:            types.ChannelRouteModeNative,
			ClientPath:           "/v1/messages",
			UpstreamPath:         "/v1/messages",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelId:         1,
			ChannelBaseUrl:    upstream.URL,
			ApiType:           constant.APITypeOpenAI,
			ApiKey:            "TOKEN",
			UpstreamModelName: "MODEL_X",
		},
	}
	maxTokens := uint(16)
	request := &dto.ClaudeRequest{
		Model:     "MODEL_X",
		Messages:  []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
		MaxTokens: &maxTokens,
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)

	usage, apiError := executeNativeTextRoute(c, info, adaptor, request, false)

	require.Nil(t, apiError)
	require.NotNil(t, usage)
	assert.Equal(t, 5, usage.TotalTokens)
	assert.Contains(t, recorder.Body.String(), `"type":"message"`)
	assert.EqualValues(t, types.RelayFormatClaude, info.GetFinalRequestRelayFormat())
}

func TestExecuteNormalizedTextRouteAppliesParamOverrideThenClaudeNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request dto.ClaudeRequest
		require.NoError(t, common.Unmarshal(body, &request))
		require.Len(t, request.Messages, 3)
		assistantContent, err := request.Messages[1].ParseContent()
		require.NoError(t, err)
		require.Len(t, assistantContent, 2)
		assert.Equal(t, "thinking", assistantContent[0].Type)
		assert.Equal(t, "a_b", assistantContent[1].Id)
		resultContent, err := request.Messages[2].ParseContent()
		require.NoError(t, err)
		require.Len(t, resultContent, 1)
		assert.Equal(t, "a_b", resultContent[0].ToolUseId)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"MODEL_X","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatClaude,
		RequestURLPath:     "/v1/messages",
		RequestedModelName: "MODEL_X",
		RoutingModelName:   "MODEL_X",
		AttemptModelName:   "MODEL_X",
		OriginModelName:    "MODEL_X",
		StartTime:          time.Now(),
		ChannelRoutePlan: &types.ChannelRoutePlan{
			ClientEndpointType:   constant.EndpointTypeAnthropic,
			UpstreamEndpointType: constant.EndpointTypeAnthropic,
			ClientRelayFormat:    types.RelayFormatClaude,
			UpstreamRelayFormat:  types.RelayFormatClaude,
			RouteMode:            types.ChannelRouteModeNormalized,
			ClientPath:           "/v1/messages",
			UpstreamPath:         "/v1/messages",
			RequestNormalizer:    relaynormalize.RequestNormalizerAnthropicMessagesCompatible,
			CapabilitySource:     "model_override",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelId:         1,
			ChannelBaseUrl:    upstream.URL,
			ApiType:           constant.APITypeOpenAI,
			ApiKey:            "TOKEN",
			UpstreamModelName: "MODEL_X",
			ParamOverride: map[string]interface{}{
				"operations": []interface{}{
					map[string]interface{}{"path": "messages.2.content.1.id", "mode": "set", "value": "a:b"},
					map[string]interface{}{"path": "messages.3.content.0.tool_use_id", "mode": "set", "value": "a:b"},
				},
			},
		},
	}
	maxTokens := uint(16)
	request := &dto.ClaudeRequest{
		Model: "MODEL_X",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "question"},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "thinking", Thinking: common.GetPointer("hidden")}}},
			{Role: "assistant", Content: []dto.ClaudeMediaMessage{
				{Type: "thinking", Thinking: common.GetPointer("kept")},
				{Type: "tool_use", Id: "call_1", Name: "lookup", Input: map[string]any{}},
			}},
			{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "tool_result", ToolUseId: "call_1", Content: "result"}}},
		},
		MaxTokens: &maxTokens,
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)

	usage, apiError := executeNativeTextRoute(c, info, adaptor, request, true)

	require.Nil(t, apiError)
	require.NotNil(t, usage)
	require.NotNil(t, info.ProtocolNormalization)
	assert.Equal(t, 1, info.ProtocolNormalization.ReasoningAssistantMessagesPreserved)
	assert.Equal(t, 1, info.ProtocolNormalization.ReasoningOnlyAssistantDropped)
	assert.Equal(t, 2, info.ProtocolNormalization.ToolIDsNormalized)
	assert.Zero(t, info.ProtocolNormalization.ToolIDCollisions)
	require.NotNil(t, info.ReasoningHistory)
	assert.Equal(t, 1, info.ReasoningHistory.PreservedMessages)
	assert.Equal(t, 1, info.ReasoningHistory.DroppedReasoningOnlyMessages)
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), info.GetFinalRequestRelayFormat())
}

func TestValidateNativeTextResponseRejectsEmptyProtocolPayloads(t *testing.T) {
	tests := []struct {
		name     string
		format   types.RelayFormat
		response any
	}{
		{name: "chat", format: types.RelayFormatOpenAI, response: &dto.OpenAITextResponse{}},
		{name: "responses", format: types.RelayFormatOpenAIResponses, response: &dto.OpenAIResponsesResponse{}},
		{name: "messages", format: types.RelayFormatClaude, response: &dto.ClaudeResponse{}},
		{name: "generate content", format: types.RelayFormatGemini, response: &dto.GeminiChatResponse{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateNativeTextResponse(test.format, test.response))
		})
	}
}

func TestValidateNativeTextStreamChunkAcceptsProtocolMarkers(t *testing.T) {
	usage := &dto.Usage{TotalTokens: 1}
	tests := []struct {
		name   string
		format types.RelayFormat
		chunk  any
	}{
		{name: "chat usage", format: types.RelayFormatOpenAI, chunk: &dto.ChatCompletionsStreamResponse{Usage: usage}},
		{name: "responses event", format: types.RelayFormatOpenAIResponses, chunk: &dto.ResponsesStreamResponse{Type: "response.created"}},
		{name: "messages event", format: types.RelayFormatClaude, chunk: &dto.ClaudeResponse{Type: "message_start"}},
		{name: "generate content usage", format: types.RelayFormatGemini, chunk: &dto.GeminiChatResponse{HasUsageMetadata: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, validateNativeTextStreamChunk(test.format, test.chunk))
		})
	}
}

func TestDecodeProtocolPayloadError(t *testing.T) {
	assert.NoError(t, decodeProtocolPayloadError([]byte(`{"type":"message_start","message":{"id":"msg_1"}}`)))
	assert.NoError(t, decodeProtocolPayloadError([]byte(`{"type":"response.completed","error":null}`)))
	require.ErrorContains(t, decodeProtocolPayloadError([]byte(`{"error":{"message":"route failed"}}`)), "route failed")
	require.ErrorContains(t, decodeProtocolPayloadError([]byte(`{"type":"response.error","message":"stream failed"}`)), "stream failed")
}

func TestProtocolUsageTextIncludesContentAndToolArguments(t *testing.T) {
	message := dto.Message{Role: "assistant", Content: "answer"}
	message.ToolCalls = json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"value\"}"}}]`)
	response := &dto.OpenAITextResponse{
		Choices: []dto.OpenAITextResponseChoice{{Message: message}},
	}
	responseText := protocolResponseUsageText(response)
	assert.Contains(t, responseText, "answer")
	assert.Contains(t, responseText, `{"q":"value"}`)

	delta := "chunk"
	chunk := &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{{
			Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
				Content: &delta,
				ToolCalls: []dto.ToolCallResponse{{
					Function: dto.FunctionResponse{Arguments: `{"id":1}`},
				}},
			},
		}},
	}
	streamText := protocolStreamChunkUsageText(chunk)
	assert.Contains(t, streamText, "chunk")
	assert.Contains(t, streamText, `{"id":1}`)
}
