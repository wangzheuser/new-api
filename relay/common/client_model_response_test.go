package common

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteClientModelJSONProtocolFields(t *testing.T) {
	info := clientModelTestRelayInfo()
	tests := []struct {
		name string
		path string
	}{
		{name: "root model", path: "model"},
		{name: "response model", path: "response.model"},
		{name: "message model", path: "message.model"},
		{name: "session model", path: "session.model"},
		{name: "snake model id", path: "model_id"},
		{name: "camel model id", path: "modelId"},
		{name: "snake model name", path: "model_name"},
		{name: "camel model name", path: "modelName"},
		{name: "snake model version", path: "model_version"},
		{name: "gemini model version", path: "modelVersion"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := []byte(`{"` + tt.path + `":"vendor-version-2099"}`)
			expected := []byte(`{"` + tt.path + `":"CLIENT_ALIAS"}`)
			if tt.path == "response.model" {
				input = []byte(`{"response":{"model":"vendor-version-2099"}}`)
				expected = []byte(`{"response":{"model":"CLIENT_ALIAS"}}`)
			}
			if tt.path == "message.model" {
				input = []byte(`{"message":{"model":"vendor-version-2099"}}`)
				expected = []byte(`{"message":{"model":"CLIENT_ALIAS"}}`)
			}
			if tt.path == "session.model" {
				input = []byte(`{"session":{"model":"vendor-version-2099"}}`)
				expected = []byte(`{"session":{"model":"CLIENT_ALIAS"}}`)
			}

			actual, changed := RewriteClientModelJSON(input, info, http.StatusOK)

			require.True(t, changed)
			assert.JSONEq(t, string(expected), string(actual))
		})
	}
}

func TestRewriteClientModelJSONPreservesGeneratedContent(t *testing.T) {
	info := clientModelTestRelayInfo()
	input := []byte(`{
		"id":"response-id",
		"choices":[{"message":{"content":"UPSTREAM_MODEL remains in normal output","model":"tool-model"}}],
		"output":[{"content":[{"text":"UPSTREAM_MODEL"}]}],
		"tool":{"arguments":{"model":"UPSTREAM_MODEL"}},
		"metadata":{"model":"UPSTREAM_MODEL"}
	}`)

	actual, changed := RewriteClientModelJSON(input, info, http.StatusOK)

	assert.False(t, changed)
	assert.Equal(t, input, actual)
}

func TestRewriteClientModelJSONDoesNotInjectMissingFields(t *testing.T) {
	info := clientModelTestRelayInfo()
	input := []byte(`{"id":"response-id","object":"chat.completion"}`)

	actual, changed := RewriteClientModelJSON(input, info, http.StatusOK)

	assert.False(t, changed)
	assert.Equal(t, input, actual)
}

func TestRewriteClientModelJSONSanitizesRecognizedErrors(t *testing.T) {
	info := clientModelTestRelayInfo()
	tests := []struct {
		name       string
		statusCode int
		input      string
		expected   string
	}{
		{
			name:       "openai error",
			statusCode: http.StatusBadGateway,
			input:      `{"error":{"message":"UPSTREAM_MODEL rejected ATTEMPT_MODEL"}}`,
			expected:   `{"error":{"message":"CLIENT_ALIAS rejected CLIENT_ALIAS"}}`,
		},
		{
			name:       "responses nested error",
			statusCode: http.StatusOK,
			input:      `{"type":"response.failed","response":{"error":{"message":"FALLBACK_MODEL unavailable"}}}`,
			expected:   `{"type":"response.failed","response":{"error":{"message":"CLIENT_ALIAS unavailable"}}}`,
		},
		{
			name:       "realtime top level message",
			statusCode: http.StatusOK,
			input:      `{"type":"error","message":"ORIGIN_MODEL failed"}`,
			expected:   `{"type":"error","message":"CLIENT_ALIAS failed"}`,
		},
		{
			name:       "normal top level message",
			statusCode: http.StatusOK,
			input:      `{"type":"message","message":"UPSTREAM_MODEL is ordinary content"}`,
			expected:   `{"type":"message","message":"UPSTREAM_MODEL is ordinary content"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, _ := RewriteClientModelJSON([]byte(tt.input), info, tt.statusCode)
			assert.JSONEq(t, tt.expected, string(actual))
		})
	}
}

func TestSanitizeClientModelErrorMessageUsesLongestBoundaryMatch(t *testing.T) {
	info := clientModelTestRelayInfo()
	info.AttemptModelName = "MODEL"
	info.OriginModelName = "MODEL-LONG"
	info.ChannelMeta.UpstreamModelName = "MODEL-LONG-V2"

	actual := SanitizeClientModelErrorMessage(
		"MODEL-LONG-V2, MODEL-LONG and MODEL failed; MODEL-LONG-V20 remains",
		info,
	)

	assert.Equal(t, "CLIENT_ALIAS, CLIENT_ALIAS and CLIENT_ALIAS failed; MODEL-LONG-V20 remains", actual)
}

func TestClientInternalModelNamesIncludesFallbackAndExcludesRequested(t *testing.T) {
	info := clientModelTestRelayInfo()
	info.AttemptModelName = "CLIENT_ALIAS"

	assert.Equal(t,
		[]string{"UPSTREAM_MODEL", "FALLBACK_MODEL", "ORIGIN_MODEL"},
		ClientInternalModelNames(info),
	)
}

func TestFilterClientModelResponseHeaders(t *testing.T) {
	info := clientModelTestRelayInfo()
	header := make(http.Header)
	header.Set("X-Upstream-Model", "UPSTREAM_MODEL")
	header.Set("X-Deployment", "region/UPSTREAM_MODEL-build")
	header.Set("X-Request-Id", "request-id")
	header.Set("Content-Length", "128")
	header.Set("ETag", `"entity-tag"`)

	FilterClientModelResponseHeaders(header, info)

	assert.Empty(t, header.Values("X-Upstream-Model"))
	assert.Empty(t, header.Values("X-Deployment"))
	assert.Equal(t, []string{"request-id"}, header.Values("X-Request-Id"))
	assert.Equal(t, []string{"128"}, header.Values("Content-Length"))
	assert.Equal(t, []string{`"entity-tag"`}, header.Values("ETag"))

	ClearTransformedEntityHeaders(header)
	assert.Empty(t, header.Values("Content-Length"))
	assert.Empty(t, header.Values("ETag"))
	assert.Equal(t, []string{"request-id"}, header.Values("X-Request-Id"))
}

// clientModelTestRelayInfo creates the internal model states used by response tests.
func clientModelTestRelayInfo() *RelayInfo {
	return &RelayInfo{
		RequestedModelName: "CLIENT_ALIAS",
		AttemptModelName:   "ATTEMPT_MODEL",
		OriginModelName:    "ORIGIN_MODEL",
		ChannelMeta: &ChannelMeta{
			UpstreamModelName: "UPSTREAM_MODEL",
		},
		ContextFallback: &ContextFallbackDecision{
			Applied:       true,
			SourceModel:   "CLIENT_ALIAS",
			FallbackModel: "FALLBACK_MODEL",
		},
	}
}
