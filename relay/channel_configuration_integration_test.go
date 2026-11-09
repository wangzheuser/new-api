package relay

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	rootdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func channelConfigurationContext(t *testing.T, channel *model.Channel, path, raw string, stream bool) (*gin.Context, *relaycommon.RelayInfo, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyIsStream, stream)
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, channel, "gpt-4o"))
	_, format, _, textProtocol := service.ResolveClientTextProtocol(path)
	if !textProtocol {
		format = types.RelayFormatOpenAIResponsesCompaction
	}
	var request kitdto.Request
	switch format {
	case types.RelayFormatOpenAI:
		request = &kitdto.GeneralOpenAIRequest{}
	case types.RelayFormatClaude:
		request = &kitdto.ClaudeRequest{}
	case types.RelayFormatGemini:
		request = &kitdto.GeminiChatRequest{}
	case types.RelayFormatOpenAIResponses:
		request = &kitdto.OpenAIResponsesRequest{}
	case types.RelayFormatOpenAIResponsesCompaction:
		request = &kitdto.OpenAIResponsesCompactionRequest{}
	}
	require.NoError(t, common.UnmarshalJsonStr(raw, request))
	request.SetModelName("gpt-4o")
	info, err := relaycommon.GenRelayInfo(c, format, request, nil)
	require.NoError(t, err)
	// Keep real response parsing and price calculation without account writes.
	info.IsChannelTest = true
	return c, info, recorder
}

func channelConfigurationRun(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		return ClaudeHelper(c, info)
	case types.RelayFormatGemini:
		return GeminiHelper(c, info)
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return ResponsesHelper(c, info)
	default:
		return TextHelper(c, info)
	}
}

func channelConfigurationTextAttempt(t *testing.T, raw string, settings rootdto.ChannelSettings, other rootdto.ChannelOtherSettings, plan *types.ChannelRoutePlan) (string, string, *relaycommon.RelayInfo) {
	t.Helper()
	type capturedRequest struct{ path, body string }
	captured := make(chan capturedRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read failed", 500)
			return
		}
		captured <- capturedRequest{r.URL.Path, string(data)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":{"message":"local audit stops before settlement","type":"invalid_request_error"}}`)
	}))
	defer upstream.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-4o")
	common.SetContextKey(c, constant.ContextKeyChannelId, 123)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "local-fixture")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, settings)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, other)
	if plan != nil {
		common.SetContextKey(c, constant.ContextKeyChannelRoutePlan, *plan)
	}
	req := &kitdto.GeneralOpenAIRequest{}
	require.NoError(t, common.UnmarshalJsonStr(raw, req))
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, req, nil)
	require.NoError(t, err)
	apiErr := TextHelper(c, info)
	require.NotNil(t, apiErr, "local fixture returns 400 before any settlement")
	select {
	case got := <-captured:
		return got.path, got.body, info
	default:
		t.Fatalf("upstream not reached: %v", apiErr)
		return "", "", info
	}
}

func TestChannelConfigurationTruncationActualHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	old := *model_setting.GetGlobalSettings()
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy.Enabled = false
	t.Cleanup(func() {
		*model_setting.GetGlobalSettings() = old
		_ = model_setting.SetContextTruncation(`{"models":{}}`)
	})
	raw := `{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"` + strings.Repeat("disposable history ", 1000) + `"},{"role":"assistant","content":"old answer"},{"role":"user","content":"latest"}]}`
	for _, scope := range []string{"channel", "global", "inherit", "off", "emergency_stop", "pass_through"} {
		t.Run(scope, func(t *testing.T) {
			require.NoError(t, model_setting.SetContextTruncation(`{"models":{}}`))
			other := rootdto.ChannelOtherSettings{}
			settings := rootdto.ChannelSettings{}
			wantMessages := 1
			if scope == "channel" {
				other.ContextTruncation = &rootdto.ContextTruncationPolicy{Models: map[string]rootdto.ContextTruncationRule{"gpt-4o": {Mode: "custom", WindowTokens: 256, OutputReserveTokens: common.GetPointer(64)}}}
			} else {
				require.NoError(t, model_setting.SetContextTruncation(`{"models":{"gpt-4o":{"mode":"custom","window_tokens":256,"output_reserve_tokens":64}}}`))
				switch scope {
				case "inherit", "off":
					other.ContextTruncation = &rootdto.ContextTruncationPolicy{Models: map[string]rootdto.ContextTruncationRule{"gpt-4o": {Mode: scope}}}
					if scope == "off" {
						wantMessages = 3
					}
				case "emergency_stop":
					require.NoError(t, model_setting.SetContextTruncation(`{"force_disabled":true,"models":{}}`))
					other.ContextTruncation = &rootdto.ContextTruncationPolicy{Models: map[string]rootdto.ContextTruncationRule{"gpt-4o": {Mode: "custom", WindowTokens: 256, OutputReserveTokens: common.GetPointer(64)}}}
					wantMessages = 3
				case "pass_through":
					settings.PassThroughBodyEnabled = true
					wantMessages = 3
				}
			}
			_, body, info := channelConfigurationTextAttempt(t, raw, settings, other, nil)
			t.Logf("scope=%s upstream_messages=%d truncation_initialized=%v", scope, len(gjson.Get(body, "messages").Array()), info.ContextTruncation != nil)
			assert.Equal(t, wantMessages, len(gjson.Get(body, "messages").Array()))
			assert.NotNil(t, info.ContextTruncation, "real handler must initialize the policy")
		})
	}
}

type channelConfigurationReservation struct {
	reserveCapture
	calls int
	err   error
}

func (r *channelConfigurationReservation) Reserve(amount int) error {
	r.calls++
	r.target = amount
	return r.err
}

func TestChannelConfigurationPreSendFailures(t *testing.T) {
	service.InitHttpClient()
	require.NoError(t, model_setting.SetContextTruncation(`{"models":{}}`))
	for _, scenario := range []string{"protected_history", "final_budget", "reserve_failure", "websocket_final_budget"} {
		t.Run(scenario, func(t *testing.T) {
			var sent atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent.Add(1); w.WriteHeader(400) }))
			defer upstream.Close()
			channel := &model.Channel{Id: 123, Type: constant.ChannelTypeOpenAI, Key: "fixture", BaseURL: &upstream.URL}
			channel.OtherSettings = `{"context_truncation":{"models":{"gpt-4o":{"mode":"custom","window_tokens":256,"output_reserve_tokens":64}}}}`
			path, raw := "/v1/chat/completions", `{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
			if scenario == "websocket_final_budget" {
				path, raw = "/v1/responses", `{"model":"gpt-4o","max_output_tokens":64,"input":[{"role":"user","content":"hello"}]}`
			}
			if scenario == "protected_history" {
				raw = strings.Replace(raw, "hello", strings.Repeat("protected ", 1000), 1)
			}
			if scenario == "final_budget" || scenario == "websocket_final_budget" {
				field := "max_tokens"
				if scenario == "websocket_final_budget" {
					field = "max_output_tokens"
				}
				override := `{"operations":[{"mode":"set","path":"` + field + `","value":255}]}`
				channel.ParamOverride = &override
			}
			c, info, _ := channelConfigurationContext(t, channel, path, raw, false)
			reservation := &channelConfigurationReservation{}
			info.PriceData.ModelRatio, info.PriceData.GroupRatioInfo.GroupRatio = 1, 1
			info.Billing = reservation
			if scenario == "reserve_failure" {
				reservation.err = errors.New("fixture reservation rejected")
			}
			var apiErr *types.NewAPIError
			if scenario == "websocket_final_budget" {
				_, apiErr = buildResponsesWSCreatePayload(c, info, *info.Request.(*kitdto.OpenAIResponsesRequest), nil, "")
			} else {
				apiErr = TextHelper(c, info)
			}
			require.NotNil(t, apiErr)
			assert.Zero(t, sent.Load())
			assert.True(t, types.IsSkipRetryError(apiErr))
			if scenario == "reserve_failure" {
				assert.ErrorContains(t, apiErr, "fixture reservation rejected")
			}
			if scenario != "protected_history" {
				assert.Equal(t, 1, reservation.calls)
			}
		})
	}
}

func TestChannelConfigurationProtocolPlanActualHandler(t *testing.T) {
	service.InitHttpClient()
	old := *model_setting.GetGlobalSettings()
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	t.Cleanup(func() { *model_setting.GetGlobalSettings() = old })
	for _, tc := range []struct {
		name     string
		endpoint constant.EndpointType
		global   bool
		want     string
	}{
		{"native_chat_over_global_upgrade", constant.EndpointTypeOpenAI, true, "/v1/chat/completions"},
		{"channel_convert_with_global_off", constant.EndpointTypeOpenAIResponse, false, "/v1/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{Enabled: tc.global, AllChannels: true, ModelPatterns: []string{".*"}}
			settings := rootdto.ChannelSettings{ProtocolPolicy: &rootdto.ChannelProtocolPolicy{Native: map[constant.EndpointType]rootdto.ProtocolCapability{tc.endpoint: {NonStream: true}}, AutoConvert: true}}
			ch := &model.Channel{Id: 123, Type: constant.ChannelTypeOpenAI}
			ch.SetSetting(settings)
			plan, err := service.PlanChannelProtocolRoute(ch, "gpt-4o", "/v1/chat/completions", false)
			require.NoError(t, err)
			require.NotNil(t, plan)
			path, _, info := channelConfigurationTextAttempt(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, settings, rootdto.ChannelOtherSettings{}, plan)
			t.Logf("mode=%s planned_path=%s global_upgrade=%v actual_path=%s", info.ChannelRoutePlan.RouteMode, plan.UpstreamPath, tc.global, path)
			assert.Equal(t, tc.want, path)
		})
	}
}

func TestChannelConfigurationTransportActualRequest(t *testing.T) {
	oldTLS := common.TLSInsecureSkipVerify
	oldTransport := http.DefaultTransport
	t.Cleanup(func() {
		common.TLSInsecureSkipVerify = oldTLS
		http.DefaultTransport = oldTransport
		service.ResetProxyClientCache()
		service.InitHttpClient()
	})
	t.Run("channel_tls_skip", func(t *testing.T) {
		common.TLSInsecureSkipVerify = false
		service.ResetProxyClientCache()
		service.InitHttpClient()
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		defer upstream.Close()
		settings := kitdto.ChannelSettings{TLSInsecureSkipVerify: true}
		control, err := service.GetHttpClientWithProxySettings("", settings)
		require.NoError(t, err)
		controlResp, err := control.Get(upstream.URL)
		require.NoError(t, err, "channel-aware factory accepts local test certificate")
		controlResp.Body.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req, err := http.NewRequest("GET", upstream.URL, http.NoBody)
		require.NoError(t, err)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: settings}}
		resp, err := relaychannel.DoRequest(c, req, info)
		if resp != nil {
			resp.Body.Close()
		}
		t.Logf("channel-aware factory succeeded; actual relay error=%v", err)
		assert.NoError(t, err, "actual relay must use channel TLS setting")
		info.ChannelSetting = kitdto.ChannelSettings{}
		req, err = http.NewRequest("GET", upstream.URL, http.NoBody)
		require.NoError(t, err)
		resp, err = relaychannel.DoRequest(c, req, info)
		if resp != nil {
			resp.Body.Close()
		}
		assert.Error(t, err, "another channel must continue verifying certificates")
	})
	t.Run("channel_http1", func(t *testing.T) {
		upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Observed-Proto", r.Proto)
			w.Header().Set("X-Observed-Connection", r.RemoteAddr)
			w.WriteHeader(204)
		}))
		upstream.EnableHTTP2 = true
		upstream.StartTLS()
		defer upstream.Close()
		trusted := oldTransport.(*http.Transport).Clone()
		trusted.TLSClientConfig = upstream.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
		http.DefaultTransport = trusted
		common.TLSInsecureSkipVerify = false
		service.ResetProxyClientCache()
		service.InitHttpClient()
		settings := kitdto.ChannelSettings{HTTPProtocol: "http1"}
		control, err := service.GetHttpClientWithProxySettings("", settings)
		require.NoError(t, err)
		controlResp, err := control.Get(upstream.URL)
		require.NoError(t, err)
		assert.Equal(t, "HTTP/1.1", controlResp.Header.Get("X-Observed-Proto"))
		controlResp.Body.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req, err := http.NewRequest("GET", upstream.URL, http.NoBody)
		require.NoError(t, err)
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: settings}}
		resp, err := relaychannel.DoRequest(c, req, info)
		require.NoError(t, err)
		defer resp.Body.Close()
		t.Logf("channel_http_protocol=http1 actual_protocol=%s", resp.Header.Get("X-Observed-Proto"))
		assert.Equal(t, "HTTP/1.1", resp.Header.Get("X-Observed-Proto"))
		info.ChannelSetting = kitdto.ChannelSettings{HTTP2ConnectionShards: 2}
		connections := map[string]struct{}{}
		for range 4 {
			req, err := http.NewRequest("GET", upstream.URL, http.NoBody)
			require.NoError(t, err)
			sharded, err := relaychannel.DoRequest(c, req, info)
			require.NoError(t, err)
			assert.Equal(t, "HTTP/2.0", sharded.Header.Get("X-Observed-Proto"))
			connections[sharded.Header.Get("X-Observed-Connection")] = struct{}{}
			sharded.Body.Close()
		}
		assert.Len(t, connections, 2, "actual relay must reuse the configured two HTTP/2 connections")
	})
}

func TestChannelConfigurationNormalizedMessages(t *testing.T) {
	service.InitHttpClient()
	require.NoError(t, model_setting.SetContextTruncation(`{"models":{}}`))
	captured := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/messages", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		captured <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture","type":"invalid_request_error"}}`)
	}))
	defer upstream.Close()
	channel := &model.Channel{Id: 123, Type: constant.ChannelTypeOpenAI, Key: "fixture", BaseURL: &upstream.URL}
	channel.SetSetting(rootdto.ChannelSettings{ProtocolPolicy: &rootdto.ChannelProtocolPolicy{Native: map[constant.EndpointType]rootdto.ProtocolCapability{
		constant.EndpointTypeAnthropic: {NonStream: true, Mode: rootdto.ProtocolHandlingModeNormalized, ReasoningHistory: types.ReasoningHistoryPolicyStrip},
	}}})
	override := `{"operations":[{"mode":"set","path":"messages.1.content.1.id","value":"a:b"},{"mode":"set","path":"messages.2.content.0.tool_use_id","value":"a:b"}]}`
	channel.ParamOverride = &override
	raw := `{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"question"},{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"result"}]}]}`
	c, info, _ := channelConfigurationContext(t, channel, "/v1/messages", raw, false)
	require.NotNil(t, ClaudeHelper(c, info))
	body := <-captured
	assert.NotContains(t, string(body), "hidden")
	assert.Equal(t, "a_b", gjson.GetBytes(body, "messages.1.content.0.id").String())
	assert.Equal(t, "a_b", gjson.GetBytes(body, "messages.2.content.0.tool_use_id").String())
}

func TestChannelConfigurationRetryIsolation(t *testing.T) {
	service.InitHttpClient()
	require.NoError(t, model_setting.SetContextTruncation(`{"models":{}}`))
	for _, passThrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("pass_through_%t", passThrough), func(t *testing.T) {
			captured := make(chan string, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				captured <- string(body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(503)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture busy","type":"server_error"}}`)
			}))
			defer upstream.Close()
			first := &model.Channel{Id: 123, Type: constant.ChannelTypeOpenAI, Key: "fixture-a", BaseURL: &upstream.URL,
				OtherSettings: `{"context_truncation":{"models":{"gpt-4o":{"mode":"custom","window_tokens":256,"output_reserve_tokens":64}}}}`}
			settings := rootdto.ChannelSettings{SystemPrompt: "PROMPT_A", SystemPromptOverride: true, PassThroughBodyEnabled: passThrough}
			if passThrough {
				settings.ProtocolPolicy = &rootdto.ChannelProtocolPolicy{Native: map[constant.EndpointType]rootdto.ProtocolCapability{constant.EndpointTypeOpenAI: {NonStream: true}}}
			}
			first.SetSetting(settings)
			raw := `{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"` + strings.Repeat("old history ", 1000) + `"},{"role":"assistant","content":"old"},{"role":"user","content":"hello"}]}`
			c, info, _ := channelConfigurationContext(t, first, "/v1/chat/completions", raw, false)
			if passThrough {
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				defer storage.Close()
			}
			require.NotNil(t, TextHelper(c, info))
			second := *first
			second.Id = 124
			second.OtherSettings = `{"context_truncation":{"models":{"gpt-4o":{"mode":"off"}}}}`
			settings.SystemPrompt = "PROMPT_B"
			second.SetSetting(settings)
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, &second, "gpt-4o"))
			info.RetryIndex = 1
			require.NotNil(t, TextHelper(c, info))
			a, b := <-captured, <-captured
			if passThrough {
				assert.JSONEq(t, raw, a)
				assert.JSONEq(t, raw, b)
			} else {
				assert.NotContains(t, a, "old history")
				assert.Contains(t, b, "old history")
				assert.Equal(t, 1, strings.Count(a, "PROMPT_A"))
				assert.Equal(t, 1, strings.Count(b, "PROMPT_B"))
				assert.NotContains(t, b, "PROMPT_A")
			}
			original, err := common.Marshal(info.Request)
			require.NoError(t, err)
			assert.NotContains(t, string(original), "PROMPT_")
			assert.Contains(t, string(original), "old history")
		})
	}
}

func TestChannelConfigurationProtocolRetry(t *testing.T) {
	service.InitHttpClient()
	global := *model_setting.GetGlobalSettings()
	model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy.Enabled = false
	t.Cleanup(func() { *model_setting.GetGlobalSettings() = global })
	captured := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture busy","type":"server_error"}}`)
	}))
	defer upstream.Close()
	first := &model.Channel{Id: 123, Type: constant.ChannelTypeOpenAI, Key: "fixture", BaseURL: &upstream.URL}
	first.SetSetting(rootdto.ChannelSettings{ProtocolPolicy: &rootdto.ChannelProtocolPolicy{
		Native: map[constant.EndpointType]rootdto.ProtocolCapability{constant.EndpointTypeOpenAIResponse: {NonStream: true}}, AutoConvert: true,
	}})
	c, info, _ := channelConfigurationContext(t, first, "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, false)
	require.NotNil(t, TextHelper(c, info))
	assert.Equal(t, "/v1/responses", <-captured)
	assert.EqualValues(t, types.RelayFormatOpenAIResponses, info.GetFinalRequestRelayFormat())
	second := *first
	second.Id = 124
	second.SetSetting(rootdto.ChannelSettings{})
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, &second, "gpt-4o"))
	info.RetryIndex = 1
	require.NotNil(t, TextHelper(c, info))
	assert.Equal(t, "/v1/chat/completions", <-captured)
	assert.EqualValues(t, types.RelayFormatOpenAI, info.GetFinalRequestRelayFormat(), "usage decoding must follow the actual retry protocol")
}

func TestChannelConfigurationResponsesCompatibility(t *testing.T) {
	service.InitHttpClient()
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact_%t", compact), func(t *testing.T) {
			captured := make(chan string, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				captured <- string(body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture","type":"invalid_request_error"}}`)
			}))
			defer upstream.Close()
			channel := &model.Channel{Id: 123, Type: constant.ChannelTypeOpenAI, Key: "fixture", BaseURL: &upstream.URL,
				OtherSettings: `{"context_truncation":{"models":{"gpt-4o":{"mode":"custom","window_tokens":256,"output_reserve_tokens":64}}}}`}
			path := "/v1/responses"
			if compact {
				path += "/compact"
			}
			raw := `{"model":"gpt-4o","previous_response_id":"previous-fixture","store":false,"input":[{"role":"user","content":"` + strings.Repeat("retained history ", 1000) + `"}]}`
			c, info, _ := channelConfigurationContext(t, channel, path, raw, false)
			require.NotNil(t, ResponsesHelper(c, info))
			body := <-captured
			assert.Contains(t, body, "retained history")
			assert.Equal(t, "previous-fixture", gjson.Get(body, "previous_response_id").String())
			if compact {
				assert.Nil(t, info.ContextTruncation)
				assert.False(t, gjson.Get(body, "max_output_tokens").Exists())
			} else {
				require.NotNil(t, info.ContextTruncation)
				assert.Equal(t, "provider_state_reference", info.ContextTruncation.Reason)
			}
		})
	}
}

func TestChannelConfigurationPromptAndCacheContext(t *testing.T) {
	service.InitHttpClient()
	require.NoError(t, model_setting.SetSystemPrompt(`{"models":{"gpt-4o":"GLOBAL_PROMPT"}}`))
	require.NoError(t, model_setting.SetCacheUsageSimulation(`{"enabled":true,"creation_trigger_percent":100,"read_trigger_percent":0}`))
	t.Cleanup(func() {
		_ = model_setting.SetSystemPrompt(`{"models":{}}`)
		_ = model_setting.SetCacheUsageSimulation(`{}`)
	})
	settings := rootdto.ChannelSettings{ModelSystemPrompts: map[string]string{"gpt-4o": "CHANNEL_PROMPT"}}
	for _, mode := range []string{"custom", "off", "inherit"} {
		t.Run(mode, func(t *testing.T) {
			other := rootdto.ChannelOtherSettings{CacheUsageSimulation: &rootdto.CacheUsageSimulationPolicy{Mode: mode, CreationTriggerPercent: common.GetPointer(0), ReadTriggerPercent: common.GetPointer(100)}}
			_, body, info := channelConfigurationTextAttempt(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, settings, other, nil)
			assert.Contains(t, body, "CHANNEL_PROMPT")
			assert.NotContains(t, body, "GLOBAL_PROMPT")
			if mode == "off" {
				assert.Nil(t, info.CacheUsageSimulation)
				return
			}
			require.NotNil(t, info.CacheUsageSimulation)
			if mode == "custom" {
				assert.Equal(t, "channel", info.CacheUsageSimulation.Source)
				assert.Equal(t, 0, *info.CacheUsageSimulation.Policy.CreationTriggerPercent)
				assert.Equal(t, 100, *info.CacheUsageSimulation.Policy.ReadTriggerPercent)
			} else {
				assert.Equal(t, "global", info.CacheUsageSimulation.Source)
			}
		})
	}
}
func TestChannelConfigurationWebSocketProxy(t *testing.T) {
	var directCalls, proxyCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directCalls.Add(1)
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			conn.Close()
		}
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		http.Error(w, "audit proxy rejects upstream", 502)
	}))
	defer proxy.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/realtime?model=gpt-4o", nil)
	info := &relaycommon.RelayInfo{
		RelayMode:      relayconstant.RelayModeRealtime,
		RelayFormat:    types.RelayFormatOpenAIRealtime,
		RequestURLPath: "/v1/realtime?model=gpt-4o",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: upstream.URL,
			ApiType: constant.APITypeOpenAI, ApiKey: "local-fixture", UpstreamModelName: "gpt-4o",
			ChannelSetting: kitdto.ChannelSettings{Proxy: proxy.URL},
		},
	}
	adaptor := GetAdaptor(constant.APITypeOpenAI)
	adaptor.Init(info)
	conn, err := relaychannel.DoWssRequest(adaptor, c, info, nil)
	if conn != nil {
		conn.Close()
	}
	t.Logf("configured_proxy_calls=%d direct_upstream_calls=%d dial_error=%v", proxyCalls.Load(), directCalls.Load(), err)
	assert.Positive(t, proxyCalls.Load(), "saved channel proxy must receive the request")
	assert.Zero(t, directCalls.Load(), "the rejecting proxy must prevent direct upstream access")
	assert.Error(t, err, "configured rejecting proxy must fail the dial")
}

func TestChannelConfigurationProtocolHTTPMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	oldGlobal, oldLog := *model_setting.GetGlobalSettings(), common.LogConsumeEnabled
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	model_setting.GetGlobalSettings().ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{Enabled: true, AllChannels: true, ModelPatterns: []string{".*"}}
	common.LogConsumeEnabled = false
	require.NoError(t, model_setting.SetContextTruncation(`{"models":{}}`))
	t.Cleanup(func() {
		*model_setting.GetGlobalSettings() = oldGlobal
		common.LogConsumeEnabled = oldLog
		constant.StreamingTimeout = oldTimeout
	})
	endpoints := []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse, constant.EndpointTypeAnthropic, constant.EndpointTypeGemini}
	paths := []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1beta/models/gpt-4o:generateContent"}
	oldHistory := strings.Repeat("disposable history ", 1000)
	bodies := []string{
		`{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"` + oldHistory + `"},{"role":"assistant","content":"old"},{"role":"user","content":"hello"}]}`,
		`{"model":"gpt-4o","max_output_tokens":64,"input":[{"role":"user","content":"` + oldHistory + `"},{"role":"assistant","content":"old"},{"role":"user","content":"hello"}]}`,
		`{"model":"gpt-4o","max_tokens":64,"messages":[{"role":"user","content":"` + oldHistory + `"},{"role":"assistant","content":"old"},{"role":"user","content":"hello"}]}`,
		`{"generationConfig":{"maxOutputTokens":64},"contents":[{"role":"user","parts":[{"text":"` + oldHistory + `"}]},{"role":"model","parts":[{"text":"old"}]},{"role":"user","parts":[{"text":"hello"}]}]}`,
	}
	responses := []string{
		`{"id":"chat_1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		`{"id":"resp_1","object":"response","model":"gpt-4o","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`,
		`{"id":"msg_1","type":"message","model":"gpt-4o","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`,
		`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`,
	}
	streams := []string{
		"data: " + `{"id":"chat_1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"},"finish_reason":null}]}` + "\n\n" +
			"data: " + `{"id":"chat_1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}` + "\n\ndata: [DONE]\n\n",
		"data: " + `{"type":"response.created","response":{"id":"resp_1","object":"response","model":"gpt-4o","status":"in_progress","output":[]}}` + "\n\n" +
			"data: " + `{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hello"}` + "\n\n" +
			"data: " + `{"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-4o","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}` + "\n\n",
		"data: " + `{"type":"message_start","message":{"id":"msg_1","type":"message","model":"gpt-4o","role":"assistant","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}` + "\n\n" +
			"data: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}` + "\n\n" +
			"data: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\ndata: {\"type\":\"message_stop\"}\n\n",
		"data: " + responses[3] + "\n\n",
	}
	for _, stream := range []bool{false, true} {
		for ci, client := range endpoints {
			for ui, upstreamEndpoint := range endpoints {
				t.Run(fmt.Sprintf("%s_via_%s_stream_%t", client, upstreamEndpoint, stream), func(t *testing.T) {
					captured := make(chan []byte, 1)
					upstreamPath := paths[ui]
					clientPath := paths[ci]
					if stream {
						upstreamPath = strings.Replace(upstreamPath, ":generateContent", ":streamGenerateContent", 1)
						clientPath = strings.Replace(clientPath, ":generateContent", ":streamGenerateContent", 1)
					}
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						assert.Equal(t, upstreamPath, r.URL.Path)
						body, err := io.ReadAll(r.Body)
						assert.NoError(t, err)
						captured <- body
						w.Header().Set("Content-Type", "application/json")
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, streams[ui])
						} else {
							_, _ = io.WriteString(w, responses[ui])
						}
					}))
					defer upstream.Close()
					channel := &model.Channel{Id: 123, Type: constant.ChannelTypeOpenAI, Key: "fixture", BaseURL: &upstream.URL}
					channel.SetSetting(rootdto.ChannelSettings{ProtocolPolicy: &rootdto.ChannelProtocolPolicy{
						Native: map[constant.EndpointType]rootdto.ProtocolCapability{upstreamEndpoint: {NonStream: true, Stream: true}}, AutoConvert: true,
					}})
					channel.OtherSettings = `{"context_truncation":{"models":{"gpt-4o":{"mode":"custom","window_tokens":256,"output_reserve_tokens":64}}}}`
					if ci == 2 && ui == 3 || ci == 3 && ui == 2 {
						plan, err := service.PlanChannelProtocolRoute(channel, "gpt-4o", clientPath, stream)
						require.NoError(t, err)
						assert.Nil(t, plan, "undeclared Claude/Gemini conversions must stay unavailable")
						return
					}
					raw := bodies[ci]
					if stream && ci != 3 {
						raw = strings.TrimSuffix(raw, "}") + `,"stream":true}`
					}
					c, info, recorder := channelConfigurationContext(t, channel, clientPath, raw, stream)
					before, err := common.Marshal(info.Request)
					require.NoError(t, err)
					require.Nil(t, channelConfigurationRun(c, info))
					finalBody := <-captured
					assert.NotContains(t, string(finalBody), "disposable history")
					assert.Contains(t, string(finalBody), "hello")
					require.NotNil(t, info.ContextTruncation)
					assert.True(t, info.ContextTruncation.Applied)
					assert.LessOrEqual(t, info.ContextTruncation.BudgetAfter, info.ContextTruncation.Budget)
					after, err := common.Marshal(info.Request)
					require.NoError(t, err)
					assert.Equal(t, string(before), string(after), "attempt processing must not change the original request")
					assert.Contains(t, recorder.Body.String(), "hello")
					assert.Equal(t, http.StatusOK, recorder.Code)
					if stream {
						require.NotNil(t, info.StreamStatus)
						assert.True(t, info.StreamStatus.IsNormalEnd())
						assert.Contains(t, recorder.Body.String(), []string{"[DONE]", "response.completed", "message_stop", "finishReason"}[ci])
					} else {
						assert.True(t, gjson.ValidBytes(recorder.Body.Bytes()))
						assert.True(t, gjson.GetBytes(recorder.Body.Bytes(), []string{"choices", "output", "content", "candidates"}[ci]).Exists())
						assert.EqualValues(t, 2, gjson.GetBytes(recorder.Body.Bytes(), []string{"usage.completion_tokens", "usage.output_tokens", "usage.output_tokens", "usageMetadata.candidatesTokenCount"}[ci]).Int())
					}
				})
			}
		}
	}
}

// channelConfigurationProxy serves one authenticated local CONNECT/SOCKS tunnel.
func channelConfigurationProxy(t *testing.T, scheme string) (*url.URL, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	if scheme == "http" || scheme == "https" {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			r.Header.Set("Authorization", r.Header.Get("Proxy-Authorization"))
			user, password, ok := r.BasicAuth()
			if !ok || user != "proxy-user" || password != "proxy-pass" {
				http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
				return
			}
			assert.Equal(t, http.MethodConnect, r.Method)
			target, err := net.Dial("tcp", r.Host)
			if !assert.NoError(t, err) {
				http.Error(w, "dial failed", 502)
				return
			}
			defer target.Close()
			conn, buffered, err := w.(http.Hijacker).Hijack()
			if !assert.NoError(t, err) {
				return
			}
			defer conn.Close()
			_, err = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
			if !assert.NoError(t, err) {
				return
			}
			go func() { _, _ = io.Copy(target, buffered); _ = target.Close() }()
			_, _ = io.Copy(conn, target)
		})
		var server *httptest.Server
		if scheme == "https" {
			server = httptest.NewTLSServer(handler)
		} else {
			server = httptest.NewServer(handler)
		}
		t.Cleanup(server.Close)
		address, err := url.Parse(server.URL)
		require.NoError(t, err)
		address.User = url.UserPassword("proxy-user", "proxy-pass")
		return address, calls
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		calls.Add(1)
		header := make([]byte, 2)
		if _, err = io.ReadFull(conn, header); err != nil {
			return
		}
		methods := make([]byte, int(header[1]))
		if _, err = io.ReadFull(conn, methods); err != nil {
			return
		}
		_, _ = conn.Write([]byte{5, 2})
		if _, err = io.ReadFull(conn, header); err != nil {
			return
		}
		user := make([]byte, int(header[1]))
		if _, err = io.ReadFull(conn, user); err != nil {
			return
		}
		if _, err = io.ReadFull(conn, header[:1]); err != nil {
			return
		}
		password := make([]byte, int(header[0]))
		if _, err = io.ReadFull(conn, password); err != nil {
			return
		}
		if string(user) != "proxy-user" || string(password) != "proxy-pass" {
			_, _ = conn.Write([]byte{1, 1})
			return
		}
		_, _ = conn.Write([]byte{1, 0})
		request := make([]byte, 4)
		if _, err = io.ReadFull(conn, request); err != nil {
			return
		}
		host := ""
		switch request[3] {
		case 1:
			ip := make([]byte, net.IPv4len)
			if _, err = io.ReadFull(conn, ip); err != nil {
				return
			}
			host = net.IP(ip).String()
		case 3:
			if _, err = io.ReadFull(conn, header[:1]); err != nil {
				return
			}
			name := make([]byte, int(header[0]))
			if _, err = io.ReadFull(conn, name); err != nil {
				return
			}
			host = string(name)
		default:
			return
		}
		if _, err = io.ReadFull(conn, header); err != nil {
			return
		}
		target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(header)))))
		if !assert.NoError(t, err) {
			return
		}
		defer target.Close()
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		go func() { _, _ = io.Copy(target, conn); _ = target.Close() }()
		_, _ = io.Copy(conn, target)
	}()
	return &url.URL{Scheme: scheme, Host: listener.Addr().String(), User: url.UserPassword("proxy-user", "proxy-pass")}, calls
}

func TestChannelConfigurationWebSocketProxySchemes(t *testing.T) {
	oldTLS := common.TLSInsecureSkipVerify
	common.TLSInsecureSkipVerify = false
	t.Cleanup(func() { common.TLSInsecureSkipVerify = oldTLS })
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		for _, authenticated := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s_authenticated_%t", scheme, authenticated), func(t *testing.T) {
				var upstreamCalls atomic.Int64
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					upstreamCalls.Add(1)
					assert.Empty(t, r.Header.Get("Proxy-Authorization"), "proxy credentials must not reach the upstream")
					upgrader := websocket.Upgrader{}
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					kind, data, err := conn.ReadMessage()
					if err == nil {
						_ = conn.WriteMessage(kind, data)
					}
				}))
				defer upstream.Close()
				address, calls := channelConfigurationProxy(t, scheme)
				if !authenticated {
					address.User = url.UserPassword("wrong-user", "wrong-password")
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
				info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRealtime, RelayFormat: types.RelayFormatOpenAIRealtime, RequestURLPath: "/v1/realtime",
					ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: upstream.URL, ApiKey: "fixture", UpstreamModelName: "gpt-4o",
						ChannelSetting: kitdto.ChannelSettings{Proxy: address.String(), TLSInsecureSkipVerify: true}}}
				adaptor := GetAdaptor(constant.APITypeOpenAI)
				adaptor.Init(info)
				conn, err := relaychannel.DoWssRequest(adaptor, c, info, nil)
				assert.EqualValues(t, 1, calls.Load())
				if !authenticated {
					require.Error(t, err)
					assert.Zero(t, upstreamCalls.Load(), "proxy rejection must never fall back to direct")
					var apiErr *types.NewAPIError
					require.ErrorAs(t, err, &apiErr)
					_, upstreamStatus := apiErr.GetUpstreamStatusCode()
					assert.False(t, upstreamStatus)
					return
				}
				require.NoError(t, err)
				defer conn.Close()
				require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("through-proxy")))
				_, body, err := conn.ReadMessage()
				require.NoError(t, err)
				assert.Equal(t, "through-proxy", string(body))
				assert.EqualValues(t, 1, upstreamCalls.Load())
			})
		}
	}
}

func TestChannelConfigurationWebSocketCancellationAndStatus(t *testing.T) {
	t.Run("cancel_upstream_handshake", func(t *testing.T) {
		started := make(chan struct{})
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		}))
		defer upstream.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRealtime, RequestURLPath: "/v1/realtime",
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: upstream.URL}}
		done := make(chan error, 1)
		go func() {
			_, err := relaychannel.DoWssRequest(GetAdaptor(constant.APITypeOpenAI), c, info, nil)
			done <- err
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("upstream handshake did not start")
		}
		cancel()
		select {
		case err := <-done:
			require.Error(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("upstream handshake ignored cancellation")
		}
	})
	t.Run("channel_tls_isolation", func(t *testing.T) {
		oldTLS := common.TLSInsecureSkipVerify
		common.TLSInsecureSkipVerify = false
		t.Cleanup(func() { common.TLSInsecureSkipVerify = oldTLS })
		upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err == nil {
				_ = conn.Close()
			}
		}))
		defer upstream.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
		for _, skip := range []bool{true, false} {
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRealtime, RequestURLPath: "/v1/realtime",
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: upstream.URL,
					ChannelSetting: kitdto.ChannelSettings{TLSInsecureSkipVerify: skip}}}
			conn, err := relaychannel.DoWssRequest(GetAdaptor(constant.APITypeOpenAI), c, info, nil)
			if skip {
				require.NoError(t, err)
				_ = conn.Close()
			} else {
				assert.Error(t, err, "TLS policy must not leak to the next channel")
			}
		}
	})
	t.Run("cancel_proxy_handshake", func(t *testing.T) {
		started := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		}))
		defer server.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRealtime, RequestURLPath: "/v1/realtime",
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "http://127.0.0.1:1",
				ChannelSetting: kitdto.ChannelSettings{Proxy: server.URL}}}
		done := make(chan error, 1)
		go func() {
			_, err := relaychannel.DoWssRequest(GetAdaptor(constant.APITypeOpenAI), c, info, nil)
			done <- err
		}()
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("proxy handshake did not start")
		}
		cancel()
		select {
		case err := <-done:
			require.Error(t, err)
			assert.ErrorIs(t, ctx.Err(), context.Canceled)
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			_, real := apiErr.GetUpstreamStatusCode()
			assert.False(t, real)
		case <-time.After(2 * time.Second):
			t.Fatal("proxy handshake ignored cancellation")
		}
	})
	t.Run("real_upstream_handshake", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "120")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
		}))
		defer upstream.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeRealtime, RequestURLPath: "/v1/realtime",
			ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: upstream.URL}}
		_, err := relaychannel.DoWssRequest(GetAdaptor(constant.APITypeOpenAI), c, info, nil)
		var apiErr *types.NewAPIError
		require.ErrorAs(t, err, &apiErr)
		status, real := apiErr.GetUpstreamStatusCode()
		assert.True(t, real)
		assert.Equal(t, http.StatusTooManyRequests, status)
		assert.Equal(t, "120", apiErr.GetUpstreamRetryAfter())
	})
}

func TestChannelConfigurationUploadClients(t *testing.T) {
	oldTLS := common.TLSInsecureSkipVerify
	common.TLSInsecureSkipVerify = false
	service.ResetProxyClientCache()
	service.InitHttpClient()
	t.Cleanup(func() {
		common.TLSInsecureSkipVerify = oldTLS
		service.ResetProxyClientCache()
		service.InitHttpClient()
	})
	for _, provider := range []string{"dify", "replicate"} {
		for _, rejected := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_proxy_rejected_%t", provider, rejected), func(t *testing.T) {
				var uploads atomic.Int64
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					uploads.Add(1)
					assert.NoError(t, r.ParseMultipartForm(1024))
					assert.Empty(t, r.Header.Get("Proxy-Authorization"))
					assert.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
					w.Header().Set("Content-Type", "application/json")
					if provider == "dify" {
						assert.Equal(t, "/v1/files/upload", r.URL.Path)
						_, _ = io.WriteString(w, `{"id":"uploaded-fixture"}`)
					} else {
						assert.Equal(t, "/v1/files", r.URL.Path)
						_, _ = io.WriteString(w, `{"urls":{"get":"https://example.test/uploaded-fixture"}}`)
					}
				}))
				defer upstream.Close()
				address, proxyCalls := channelConfigurationProxy(t, "http")
				if rejected {
					address.User = url.UserPassword("wrong-user", "wrong-password")
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "fixture-key",
					ChannelSetting: kitdto.ChannelSettings{Proxy: address.String(), TLSInsecureSkipVerify: true}}}
				var converted any
				var err error
				if provider == "dify" {
					adaptor := GetAdaptor(constant.APITypeDify)
					converted, err = adaptor.ConvertOpenAIRequest(c, info, &kitdto.GeneralOpenAIRequest{
						User:     []byte(`"fixture-user"`),
						Messages: []kitdto.Message{{Role: "user", Content: []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,aGVsbG8="}}}}},
					})
				} else {
					var form bytes.Buffer
					writer := multipart.NewWriter(&form)
					part, writeErr := writer.CreateFormFile("image", "fixture.png")
					require.NoError(t, writeErr)
					_, writeErr = io.WriteString(part, "fixture-image")
					require.NoError(t, writeErr)
					require.NoError(t, writer.Close())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &form)
					c.Request.Header.Set("Content-Type", writer.FormDataContentType())
					info.RelayMode = relayconstant.RelayModeImagesEdits
					adaptor := GetAdaptor(constant.APITypeReplicate)
					converted, err = adaptor.ConvertImageRequest(c, info, kitdto.ImageRequest{Model: "black-forest-labs/flux-1.1-pro", Prompt: "fixture"})
				}
				assert.EqualValues(t, 1, proxyCalls.Load())
				if rejected {
					assert.Zero(t, uploads.Load(), "failed upload proxy must not be bypassed")
					if provider == "replicate" {
						assert.Error(t, err)
					}
					return
				}
				require.NoError(t, err)
				assert.EqualValues(t, 1, uploads.Load())
				data, err := common.Marshal(converted)
				require.NoError(t, err)
				assert.Contains(t, string(data), "uploaded-fixture")
			})
		}
	}
}
