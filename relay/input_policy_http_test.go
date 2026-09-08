package relay

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestInputPolicyHTTPMatrix verifies text/image trimming and unchanged cache eligibility across HTTP/SSE routes.
func TestInputPolicyHTTPMatrix(t *testing.T) {
	service.InitHttpClient()
	formats := []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini}
	paths := []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1beta/models/MODEL_X:generateContent"}

	old := strings.Repeat("old context with disposable history ", 500)
	requests := []string{
		fmt.Sprintf(`{"model":"MODEL_X","max_tokens":64,"messages":[{"role":"user","content":"%s"},{"role":"assistant","content":"old answer"},{"role":"user","content":"hello"}]}`, old),
		fmt.Sprintf(`{"model":"MODEL_X","max_output_tokens":64,"input":[{"role":"user","content":"%s"},{"role":"assistant","content":"old answer"},{"role":"user","content":"hello"}]}`, old),
		fmt.Sprintf(`{"model":"MODEL_X","max_tokens":64,"messages":[{"role":"user","content":"%s"},{"role":"assistant","content":"old answer"},{"role":"user","content":"hello"}]}`, old),
		fmt.Sprintf(`{"model":"MODEL_X","generationConfig":{"maxOutputTokens":64},"contents":[{"role":"user","parts":[{"text":"%s"}]},{"role":"model","parts":[{"text":"old answer"}]},{"role":"user","parts":[{"text":"hello"}]}]}`, old),
	}

	responses := []string{
		`{"id":"chat_1","object":"chat.completion","created":1,"model":"MODEL_X","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		`{"id":"resp_1","object":"response","created_at":1,"model":"MODEL_X","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`,
		`{"id":"msg_1","type":"message","model":"MODEL_X","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`,
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`,
	}

	streams := []string{
		"data: " + `{"id":"chat_1","object":"chat.completion.chunk","model":"MODEL_X","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}` + "\n\n" +
			"data: " + `{"id":"chat_1","object":"chat.completion.chunk","model":"MODEL_X","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}` + "\n\ndata: [DONE]\n\n",
		"data: " + `{"type":"response.created","response":{"id":"resp_1","object":"response","model":"MODEL_X","status":"in_progress","output":[]}}` + "\n\n" +
			"data: " + `{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hello"}` + "\n\n" +
			"data: " + `{"type":"response.completed","response":{"id":"resp_1","object":"response","model":"MODEL_X","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}` + "\n\n",
		"data: " + `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"MODEL_X","usage":{"input_tokens":3,"output_tokens":0}}}` + "\n\n" +
			"data: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}` + "\n\n" +
			"data: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\ndata: {\"type\":\"message_stop\"}\n\n",
		"data: " + `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}` + "\n\n",
	}
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jWZkAAAAASUVORK5CYII="
	for _, images := range []bool{false, true} {
		bodies := append([]string(nil), requests...)
		if images {
			bodies[0] = strings.Replace(bodies[0], `"content":"hello"`, `"content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"data:image/png;base64,`+png+`"}}]`, 1)
			bodies[1] = strings.Replace(bodies[1], `"content":"hello"`, `"content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,`+png+`"}]`, 1)
			bodies[2] = strings.Replace(bodies[2], `"content":"hello"`, `"content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+png+`"}}]`, 1)
			bodies[3] = strings.Replace(bodies[3], `{"text":"hello"}`, `{"text":"hello"},{"inlineData":{"mimeType":"image/png","data":"`+png+`"}}`, 1)
		}
		for _, stream := range []bool{false, true} {
			for ci, client := range formats {
				for ui, upstreamFormat := range formats {
					t.Run(string(client)+"_via_"+string(upstreamFormat)+fmt.Sprint(stream)+"_images_"+fmt.Sprint(images), func(t *testing.T) {
						calls := 0
						upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls++
							expectedPath := paths[ui]
							if stream && ui == 3 {
								expectedPath = strings.Replace(expectedPath, ":generateContent", ":streamGenerateContent", 1)
							}
							assert.Equal(t, expectedPath, r.URL.Path)
							body, err := io.ReadAll(r.Body)
							assert.NoError(t, err)
							assert.Contains(t, string(body), "hello")
							if images {
								assert.Contains(t, string(body), png)
							}
							assert.NotContains(t, string(body), "disposable history")
							assert.Len(t, gjson.GetBytes(body, []string{"messages", "input", "messages", "contents"}[ui]).Array(), 1)
							var decoded map[string]any
							assert.NoError(t, common.Unmarshal(body, &decoded))
							field := []string{"messages", "input", "messages", "contents"}[ui]
							assert.NotEmpty(t, decoded[field])
							if stream {
								w.Header().Set("Content-Type", "text/event-stream")
								_, _ = io.WriteString(w, streams[ui])
							} else {
								w.Header().Set("Content-Type", "application/json")
								_, _ = io.WriteString(w, responses[ui])
							}
						}))
						defer upstream.Close()
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodPost, paths[ci], strings.NewReader(bodies[ci]))
						info := convertedResponsesViaChatTestInfo(upstream.URL, stream)
						info.RelayFormat = client
						info.RequestURLPath = paths[ci]
						ce, _, cm, _ := service.ResolveClientTextProtocol(paths[ci])
						ue, _, um, _ := service.ResolveClientTextProtocol(paths[ui])
						info.RelayMode = cm
						plan := info.ChannelRoutePlan
						plan.ClientRelayFormat = client
						plan.UpstreamRelayFormat = upstreamFormat
						plan.ClientEndpointType = ce
						plan.UpstreamEndpointType = ue
						plan.ClientRelayMode = cm
						plan.UpstreamRelayMode = um
						plan.ClientPath = paths[ci]
						plan.UpstreamPath = paths[ui]
						var request dto.Request
						switch client {
						case types.RelayFormatOpenAI:
							request = &dto.GeneralOpenAIRequest{}
						case types.RelayFormatOpenAIResponses:
							request = &dto.OpenAIResponsesRequest{}
						case types.RelayFormatClaude:
							request = &dto.ClaudeRequest{}
						case types.RelayFormatGemini:
							request = &dto.GeminiChatRequest{}
						}
						require.NoError(t, common.Unmarshal([]byte(bodies[ci]), request))

						info.ChannelOtherSettings.ContextTruncation = &dto.ContextTruncationPolicy{Models: map[string]dto.ContextTruncationRule{"MODEL_X": {Mode: "custom", WindowTokens: 2048, OutputReserveTokens: common.GetPointer(64)}}}
						info.ChannelOtherSettings.CacheUsageSimulation = &dto.CacheUsageSimulationPolicy{Mode: "custom"}
						info.InitInputPolicyState()
						require.Nil(t, prepareTextInputPolicies(c, info, request, false))
						require.True(t, info.ContextTruncation.Applied)
						assert.Greater(t, info.ContextTruncation.Before, info.ContextTruncation.Budget)
						assert.LessOrEqual(t, info.ContextTruncation.After, info.ContextTruncation.Budget)
						adaptor := GetAdaptor(constant.APITypeOpenAI)
						adaptor.Init(info)
						var usage *dto.Usage
						var apiErr *types.NewAPIError
						if client == upstreamFormat {
							plan.RouteMode = types.ChannelRouteModeNative
							usage, apiErr = executeNativeTextRoute(c, info, adaptor, request, false)
						} else {
							route, ok := relayconvert.ResolveRoute(client, upstreamFormat, stream)
							require.True(t, ok)
							plan.RequestConverter = route.RequestConverter
							plan.ResponseConverter = route.ResponseConverter
							usage, apiErr = executeConvertedTextRoute(c, info, adaptor, request)
						}
						require.Nil(t, apiErr)
						require.NotNil(t, usage)
						assert.Equal(t, 1, calls)
						if images {
							assert.Equal(t, "unknown", info.CacheUsageSimulation.Presence)
							assert.False(t, info.CacheUsageSimulation.HasInput)
						} else {
							assert.Equal(t, "absent", info.CacheUsageSimulation.Presence)
							assert.True(t, info.CacheUsageSimulation.HasInput)
						}
						if images {
							assert.Equal(t, "multimodal", info.CacheUsageSimulation.Reason)
						} else {
							assert.Empty(t, info.CacheUsageSimulation.Reason)
						}
						assert.Contains(t, recorder.Body.String(), "hello")
						assert.Equal(t, http.StatusOK, recorder.Code)
						if stream {
							return
						}
						var result map[string]any
						require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
						assert.NotEmpty(t, result[[]string{"choices", "output", "content", "candidates"}[ci]])
					})
				}
			}
		}

	}
}
