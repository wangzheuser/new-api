package channel

import (
	"context"
	"github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTaskAPIRequestInheritsClientCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(requestContext)

	upstream, err := newTaskAPIRequest(c, "https://provider.example/tasks", nil)
	require.NoError(t, err)
	cancel()

	require.ErrorIs(t, upstream.Context().Err(), context.Canceled)
}

func TestProcessHeaderOverride_ChannelTestSkipsPassthroughRules(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Empty(t, headers)
}

func TestProcessHeaderOverride_ChannelTestSkipsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	_, ok := headers["x-upstream-trace"]
	require.False(t, ok)
}

func TestProcessHeaderOverride_NonTestKeepsClientHeaderPlaceholder(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Upstream-Trace": "{client_header:X-Trace-Id}",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-upstream-trace"])
}

func TestProcessHeaderOverride_RuntimeOverrideIsFinalHeaderMap(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		IsChannelTest:             false,
		UseRuntimeHeadersOverride: true,
		RuntimeHeadersOverride: map[string]any{
			"x-static":  "runtime-value",
			"x-runtime": "runtime-only",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
				"X-Legacy": "legacy-only",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "runtime-value", headers["x-static"])
	require.Equal(t, "runtime-only", headers["x-runtime"])
	_, exists := headers["x-legacy"]
	require.False(t, exists)
}

func TestProcessHeaderOverride_PassthroughSkipsAcceptEncoding(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Request.Header.Set("X-Trace-Id", "trace-123")
	ctx.Request.Header.Set("Accept-Encoding", "gzip")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			HeadersOverride: map[string]any{
				"*": "",
			},
		},
	}

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "trace-123", headers["x-trace-id"])

	_, hasAcceptEncoding := headers["accept-encoding"]
	require.False(t, hasAcceptEncoding)
}

func TestProcessHeaderOverride_PassHeadersTemplateSetsRuntimeHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("Originator", "Codex CLI")
	ctx.Request.Header.Set("Session_id", "sess-123")

	info := &relaycommon.RelayInfo{
		IsChannelTest: false,
		RequestHeaders: map[string]string{
			"Originator": "Codex CLI",
			"Session_id": "sess-123",
		},
		ChannelMeta: &relaycommon.ChannelMeta{
			ParamOverride: map[string]any{
				"operations": []any{
					map[string]any{
						"mode":  "pass_headers",
						"value": []any{"Originator", "Session_id", "X-Codex-Beta-Features"},
					},
				},
			},
			HeadersOverride: map[string]any{
				"X-Static": "legacy-value",
			},
		},
	}

	_, err := relaycommon.ApplyParamOverrideWithRelayInfo([]byte(`{"model":"gpt-4.1"}`), info)
	require.NoError(t, err)
	require.True(t, info.UseRuntimeHeadersOverride)
	require.Equal(t, "Codex CLI", info.RuntimeHeadersOverride["originator"])
	require.Equal(t, "sess-123", info.RuntimeHeadersOverride["session_id"])
	_, exists := info.RuntimeHeadersOverride["x-codex-beta-features"]
	require.False(t, exists)
	require.Equal(t, "legacy-value", info.RuntimeHeadersOverride["x-static"])

	headers, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	require.Equal(t, "Codex CLI", headers["originator"])
	require.Equal(t, "sess-123", headers["session_id"])
	_, exists = headers["x-codex-beta-features"]
	require.False(t, exists)

	upstreamReq := httptest.NewRequest(http.MethodPost, "https://example.com/v1/responses", nil)
	applyHeaderOverrideToRequest(upstreamReq, headers)
	require.Equal(t, "Codex CLI", upstreamReq.Header.Get("Originator"))
	require.Equal(t, "sess-123", upstreamReq.Header.Get("Session_id"))
	require.Empty(t, upstreamReq.Header.Get("X-Codex-Beta-Features"))
}

func TestToWebSocketURL(t *testing.T) {
	for input, want := range map[string]string{
		"https://api.openai.com/v1/responses":             "wss://api.openai.com/v1/responses",
		"http://127.0.0.1:3000/v1/responses":              "ws://127.0.0.1:3000/v1/responses",
		"wss://chatgpt.com/backend-api/codex/responses":   "wss://chatgpt.com/backend-api/codex/responses",
		"ws://127.0.0.1:3000/backend-api/codex/responses": "ws://127.0.0.1:3000/backend-api/codex/responses",
	} {
		assert.Equal(t, want, toWebSocketURL(input), input)
	}
}

// The embedded interface supplies unused adaptor methods for the real HTTP request path.
type agentPresetTestAdaptor struct {
	Adaptor
	url string
}

func (a agentPresetTestAdaptor) GetRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.url, nil
}
func (a agentPresetTestAdaptor) SetupRequestHeader(_ *gin.Context, headers *http.Header, _ *relaycommon.RelayInfo) error {
	headers.Set("Authorization", "Bearer upstream-key")
	headers.Set("Content-Type", "application/json")
	return nil
}

func TestAgentHeaderPresetUpstreamRequests(t *testing.T) {
	for _, tc := range []struct {
		name, client, ua, originator, beta string
		testRequest                        bool
		overrides                          map[string]any
		wantUA, wantOriginator, wantBeta   string
	}{
		{name: "curl uses codex defaults despite wildcard", client: "codex", ua: "curl/8.0", overrides: map[string]any{"*": true}, wantUA: "codex_cli_rs/0.172.0", wantOriginator: "codex_cli_rs"},
		{name: "native codex retains matching identity", client: "codex", ua: "codex-tui/0.180.0", originator: "codex-tui", wantUA: "codex-tui/0.180.0", wantOriginator: "codex-tui"},
		{name: "mismatched codex identity falls back as a group", client: "codex", ua: "codex-tui/0.180.0", originator: "codex_cli_rs", wantUA: "codex_cli_rs/0.172.0", wantOriginator: "codex_cli_rs"},
		{name: "other client uses target preset", client: "codex", ua: "claude-cli/2.1.332 (external, cli)", wantUA: "codex_cli_rs/0.172.0", wantOriginator: "codex_cli_rs"},
		{name: "native claude does not regain omitted beta", client: "claude", ua: "claude-cli/2.1.340 (external, cli)", wantUA: "claude-cli/2.1.340 (external, cli)"},
		{name: "sdk retains requested beta with default identity", client: "claude", ua: "Anthropic/Python 1.0", beta: "requested-capability", wantUA: "claude-cli/2.1.332 (external, cli)", wantBeta: "requested-capability"},
		{name: "channel test uses defaults without browser headers", client: "codex", ua: "codex-tui/0.180.0", originator: "codex-tui", testRequest: true, wantUA: "codex_cli_rs/0.172.0", wantOriginator: "codex_cli_rs"},
		{name: "explicit override wins", client: "codex", ua: "curl/8.0", overrides: map[string]any{"User-Agent": "operator-agent"}, wantUA: "operator-agent", wantOriginator: "codex_cli_rs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, bodyPassthrough := range []bool{false, true} {
				preset := &dto.AgentHeaderPreset{Client: tc.client, Headers: map[string]string{"user-agent": "codex_cli_rs/0.172.0", "originator": "codex_cli_rs", "version": "0.172.0"}}
				if tc.client == "claude" {
					preset.Headers = map[string]string{"user-agent": "claude-cli/2.1.332 (external, cli)", "x-app": "cli", "anthropic-beta": "preset-beta"}
				}
				require.NoError(t, (dto.ChannelSettings{AgentHeaderPreset: preset}).ValidateAgentHeaderPreset())
				var received http.Header
				var body string
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					received = r.Header.Clone()
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					body = string(raw)
					w.WriteHeader(http.StatusOK)
				}))
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				ctx.Request.Header.Set("User-Agent", tc.ua)
				ctx.Request.Header.Set("originator", tc.originator)
				ctx.Request.Header.Set("anthropic-beta", tc.beta)
				ctx.Request.Header.Set("Authorization", "Bearer caller-key")
				ctx.Request.Header.Set("Cookie", "session=caller")
				ctx.Request.Header.Set("Session_id", "thread-A")
				overrides := map[string]any{"X-Operator": "kept"}
				for k, v := range tc.overrides {
					overrides[k] = v
				}
				info := &relaycommon.RelayInfo{IsChannelTest: tc.testRequest, ChannelMeta: &relaycommon.ChannelMeta{HeadersOverride: overrides, ChannelSetting: kitdto.ChannelSettings{AgentHeaderPreset: preset, PassThroughBodyEnabled: bodyPassthrough}}}
				response, err := DoApiRequest(agentPresetTestAdaptor{url: upstream.URL}, ctx, info, strings.NewReader(`{"model":"example"}`))
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				upstream.Close()
				assert.Equal(t, http.StatusOK, response.StatusCode)
				assert.Equal(t, tc.wantUA, received.Get("User-Agent"))
				assert.Equal(t, tc.wantOriginator, received.Get("originator"))
				assert.Equal(t, tc.wantBeta, received.Get("anthropic-beta"))
				if tc.name == "native codex retains matching identity" {
					assert.Equal(t, "thread-A", received.Get("Session_id"))
				}
				assert.Equal(t, "Bearer upstream-key", received.Get("Authorization"))
				assert.Empty(t, received.Get("Cookie"))
				assert.Equal(t, "kept", received.Get("X-Operator"))
				assert.Equal(t, `{"model":"example"}`, body)
			}
		})
	}
}

func TestAgentHeaderPresetValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		preset dto.AgentHeaderPreset
	}{
		{"unknown client", dto.AgentHeaderPreset{Client: "unknown"}},
		{"credential header", dto.AgentHeaderPreset{Client: "codex", Headers: map[string]string{"Authorization": "secret"}}},
		{"header injection", dto.AgentHeaderPreset{Client: "codex", Headers: map[string]string{"User-Agent": "client\r\nX-Extra: injected"}}},
		{"duplicate casing", dto.AgentHeaderPreset{Client: "codex", Headers: map[string]string{"User-Agent": "a", "user-agent": "b"}}},
		{"missing identity", dto.AgentHeaderPreset{Client: "claude", Headers: map[string]string{"x-app": "cli"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, (dto.ChannelSettings{AgentHeaderPreset: &tc.preset}).ValidateAgentHeaderPreset())
		})
	}
}

func TestAgentHeaderPresetAttemptIsolation(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Request.Header.Set("User-Agent", "curl/8.0")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: kitdto.ChannelSettings{AgentHeaderPreset: &dto.AgentHeaderPreset{Client: "codex", Headers: map[string]string{"user-agent": "codex_cli_rs/0.172.0"}}}}}
	first, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	info.ChannelSetting.AgentHeaderPreset = &dto.AgentHeaderPreset{Client: "claude", Headers: map[string]string{"user-agent": "claude-cli/2.1.332 (external, cli)"}}
	second, err := processHeaderOverride(info, ctx)
	require.NoError(t, err)
	assert.Equal(t, "codex_cli_rs/0.172.0", first["user-agent"])
	assert.Equal(t, "claude-cli/2.1.332 (external, cli)", second["user-agent"])
	assert.Equal(t, "curl/8.0", ctx.Request.UserAgent())
}
