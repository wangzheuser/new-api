package dto

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelSettingsResolveSystemPrompt(t *testing.T) {
	settings := ChannelSettings{
		SystemPrompt:         "channel default",
		SystemPromptOverride: false,
		ModelSystemPrompts: map[string]string{
			"model-a": "model prompt",
		},
	}

	prompt, prepend := settings.ResolveSystemPrompt("model-a")
	assert.Equal(t, "model prompt", prompt)
	assert.True(t, prepend)

	prompt, prepend = settings.ResolveSystemPrompt("model-b")
	assert.Equal(t, "channel default", prompt)
	assert.False(t, prepend)
}

func TestChannelSettingsValidateSystemPrompts(t *testing.T) {
	require.NoError(t, (ChannelSettings{ModelSystemPrompts: map[string]string{
		"model-a": "prompt",
	}}).ValidateSystemPrompts())

	tests := []struct {
		name   string
		model  string
		prompt string
	}{
		{name: "empty model", model: "", prompt: "prompt"},
		{name: "surrounding whitespace", model: " model-a", prompt: "prompt"},
		{name: "model too long", model: strings.Repeat("a", 256), prompt: "prompt"},
		{name: "empty prompt", model: "model-a", prompt: "  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (ChannelSettings{ModelSystemPrompts: map[string]string{
				tt.model: tt.prompt,
			}}).ValidateSystemPrompts()
			require.Error(t, err)
		})
	}

	tooMany := make(map[string]string, MaxModelSystemPromptEntries+1)
	for i := 0; i <= MaxModelSystemPromptEntries; i++ {
		tooMany[fmt.Sprintf("model-%d", i)] = "prompt"
	}
	require.Error(t, (ChannelSettings{ModelSystemPrompts: tooMany}).ValidateSystemPrompts())
	require.Error(t, (ChannelSettings{ModelSystemPrompts: map[string]string{
		"model-a": strings.Repeat("a", MaxModelSystemPromptBytes+1),
	}}).ValidateSystemPrompts())
}

func TestChannelSettingsResolveSystemPromptForFallbackAttempt(t *testing.T) {
	settings := ChannelSettings{
		SystemPrompt: "channel default",
		ModelSystemPrompts: map[string]string{
			"model-a": "requested prompt",
			"model-b": "attempt prompt",
		},
	}

	prompt, prepend, source, matchedModel := settings.ResolveSystemPromptForAttempt("model-a", "model-b", true)
	assert.Equal(t, "requested prompt", prompt)
	assert.True(t, prepend)
	assert.Equal(t, "model_requested", source)
	assert.Equal(t, "model-a", matchedModel)

	delete(settings.ModelSystemPrompts, "model-a")
	prompt, prepend, source, matchedModel = settings.ResolveSystemPromptForAttempt("model-a", "model-b", true)
	assert.Equal(t, "attempt prompt", prompt)
	assert.True(t, prepend)
	assert.Equal(t, "model_attempt", source)
	assert.Equal(t, "model-b", matchedModel)
}

func TestChannelSettingsValidateContextFallbacks(t *testing.T) {
	valid := ChannelSettings{ModelContextFallbacks: map[string]ModelContextFallback{
		"model-a": {
			SourceContextWindowTokens:   262144,
			FallbackModel:               "model-b",
			FallbackContextWindowTokens: 1048576,
			RouteMode:                   ContextFallbackModeCross,
			TargetChannelIDs:            []int{2, 3},
		},
	}}
	require.NoError(t, valid.ValidateContextFallbacks())
	rule, ok := valid.ResolveContextFallback("model-a")
	require.True(t, ok)
	assert.Equal(t, DefaultContextThreshold, rule.EffectiveThresholdPercent())
	assert.Equal(t, int64(235929), rule.ThresholdTokens())
	assert.Greater(t, (ModelContextFallback{SourceContextWindowTokens: math.MaxInt64, ThresholdPercent: 90}).ThresholdTokens(), int64(0))

	tests := []struct {
		name string
		rule ModelContextFallback
	}{
		{name: "source window", rule: ModelContextFallback{FallbackModel: "model-b", FallbackContextWindowTokens: 1, RouteMode: ContextFallbackModeCross}},
		{name: "threshold", rule: ModelContextFallback{SourceContextWindowTokens: 1, ThresholdPercent: 101, FallbackModel: "model-b", FallbackContextWindowTokens: 1, RouteMode: ContextFallbackModeCross}},
		{name: "same model", rule: ModelContextFallback{SourceContextWindowTokens: 1, FallbackModel: "model-a", FallbackContextWindowTokens: 1, RouteMode: ContextFallbackModeCross}},
		{name: "fallback window", rule: ModelContextFallback{SourceContextWindowTokens: 1, FallbackModel: "model-b", RouteMode: ContextFallbackModeCross}},
		{name: "route mode", rule: ModelContextFallback{SourceContextWindowTokens: 1, FallbackModel: "model-b", FallbackContextWindowTokens: 1, RouteMode: "invalid"}},
		{name: "same target ids", rule: ModelContextFallback{SourceContextWindowTokens: 1, FallbackModel: "model-b", FallbackContextWindowTokens: 1, RouteMode: ContextFallbackModeSame, TargetChannelIDs: []int{2}}},
		{name: "duplicate target ids", rule: ModelContextFallback{SourceContextWindowTokens: 1, FallbackModel: "model-b", FallbackContextWindowTokens: 1, RouteMode: ContextFallbackModeCross, TargetChannelIDs: []int{2, 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, (ChannelSettings{ModelContextFallbacks: map[string]ModelContextFallback{"model-a": tt.rule}}).ValidateContextFallbacks())
		})
	}
}

func TestChannelProtocolPolicyValidateAndResolveModelOverride(t *testing.T) {
	policy := ChannelProtocolPolicy{
		Native: map[constant.EndpointType]ProtocolCapability{
			constant.EndpointTypeOpenAI: {NonStream: true, Stream: true},
		},
		ModelOverrides: map[string]ModelProtocolProfile{
			"MODEL_X": {
				Native: map[constant.EndpointType]ProtocolCapability{
					constant.EndpointTypeOpenAIResponse: {NonStream: true},
				},
			},
		},
		AutoConvert: true,
	}
	require.NoError(t, policy.Validate())
	assert.Equal(t, ProtocolConversionQualityFair, policy.EffectiveMaxQuality())

	native, source := policy.NativeForModel("MODEL_X")
	assert.Equal(t, "model_override", source)
	assert.Contains(t, native, constant.EndpointTypeOpenAIResponse)
	assert.NotContains(t, native, constant.EndpointTypeOpenAI)

	native, source = policy.NativeForModel("MODEL_Y")
	assert.Equal(t, "channel_default", source)
	assert.Contains(t, native, constant.EndpointTypeOpenAI)
	assert.Equal(t, ProtocolHandlingModeNative, native[constant.EndpointTypeOpenAI].EffectiveMode())
}

func TestChannelProtocolPolicyValidateNormalizedSupportedEndpoints(t *testing.T) {
	valid := ChannelProtocolPolicy{Native: map[constant.EndpointType]ProtocolCapability{
		constant.EndpointTypeAnthropic: {
			NonStream:        true,
			Stream:           true,
			Mode:             ProtocolHandlingModeNormalized,
			ReasoningHistory: types.ReasoningHistoryPolicyStrip,
		},
		constant.EndpointTypeOpenAIResponse: {
			NonStream: true,
			Stream:    true,
			Mode:      ProtocolHandlingModeNormalized,
		},
	}}
	require.NoError(t, valid.Validate())
	assert.True(t, valid.HasNormalizedCapability())
	assert.Equal(t, types.ReasoningHistoryPolicyStrip, valid.Native[constant.EndpointTypeAnthropic].EffectiveReasoningHistoryPolicy())

	invalidEndpoint := ChannelProtocolPolicy{Native: map[constant.EndpointType]ProtocolCapability{
		constant.EndpointTypeOpenAI: {NonStream: true, Mode: ProtocolHandlingModeNormalized},
	}}
	require.ErrorContains(t, invalidEndpoint.Validate(), "unsupported for openai")

	invalidMode := ChannelProtocolPolicy{Native: map[constant.EndpointType]ProtocolCapability{
		constant.EndpointTypeAnthropic: {NonStream: true, Mode: "rewritten"},
	}}
	require.ErrorContains(t, invalidMode.Validate(), "invalid protocol handling mode")

	invalidReasoningPolicy := ChannelProtocolPolicy{Native: map[constant.EndpointType]ProtocolCapability{
		constant.EndpointTypeAnthropic: {NonStream: true, Mode: ProtocolHandlingModeNormalized, ReasoningHistory: "archive"},
	}}
	require.ErrorContains(t, invalidReasoningPolicy.Validate(), "invalid reasoning history policy")

	reasoningOnNative := ChannelProtocolPolicy{Native: map[constant.EndpointType]ProtocolCapability{
		constant.EndpointTypeAnthropic: {NonStream: true, ReasoningHistory: types.ReasoningHistoryPolicyStrip},
	}}
	require.ErrorContains(t, reasoningOnNative.Validate(), "only supported for normalized anthropic")

	reasoningOnResponses := ChannelProtocolPolicy{Native: map[constant.EndpointType]ProtocolCapability{
		constant.EndpointTypeOpenAIResponse: {NonStream: true, Mode: ProtocolHandlingModeNormalized, ReasoningHistory: types.ReasoningHistoryPolicyStrip},
	}}
	require.ErrorContains(t, reasoningOnResponses.Validate(), "only supported for normalized anthropic")
}

func TestChannelProtocolPolicyValidateRejectsInvalidConfigurations(t *testing.T) {
	validNative := map[constant.EndpointType]ProtocolCapability{
		constant.EndpointTypeOpenAI: {NonStream: true},
	}
	tests := []struct {
		name   string
		policy ChannelProtocolPolicy
	}{
		{name: "empty native", policy: ChannelProtocolPolicy{}},
		{name: "unsupported endpoint", policy: ChannelProtocolPolicy{Native: map[constant.EndpointType]ProtocolCapability{constant.EndpointTypeEmbeddings: {NonStream: true}}}},
		{name: "empty capability", policy: ChannelProtocolPolicy{Native: map[constant.EndpointType]ProtocolCapability{constant.EndpointTypeOpenAI: {}}}},
		{name: "invalid quality", policy: ChannelProtocolPolicy{Native: validNative, MaxQuality: "discouraged"}},
		{name: "empty model override", policy: ChannelProtocolPolicy{Native: validNative, ModelOverrides: map[string]ModelProtocolProfile{"MODEL_X": {}}}},
		{name: "invalid model name", policy: ChannelProtocolPolicy{Native: validNative, ModelOverrides: map[string]ModelProtocolProfile{" MODEL_X": {Native: validNative}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, tt.policy.Validate())
		})
	}

	tooMany := make(map[string]ModelProtocolProfile, MaxModelProtocolOverrides+1)
	for i := 0; i <= MaxModelProtocolOverrides; i++ {
		tooMany[fmt.Sprintf("MODEL_%d", i)] = ModelProtocolProfile{Native: validNative}
	}
	require.Error(t, (ChannelProtocolPolicy{Native: validNative, ModelOverrides: tooMany}).Validate())
}

func TestAdvancedCustomValidateResponsesToChatConverterPath(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
		},
	}
	require.NoError(t, valid.Validate())

	validGemini := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
			},
		},
	}
	require.NoError(t, validGemini.Validate())

	tests := []struct {
		name         string
		incomingPath string
	}{
		{name: "chat completions", incomingPath: "/v1/chat/completions"},
		{name: "responses compact", incomingPath: "/v1/responses/compact"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: tt.incomingPath,
						UpstreamPath: "/v1/chat/completions",
						Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
					},
				},
			}
			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "converter does not match incoming_path")
		})
	}
}

func TestAdvancedCustomValidateModelListRouteConstraints(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: AdvancedCustomModelListPath,
				UpstreamPath: "https://upstream.example/custom/models",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		routes []AdvancedCustomRoute
		want   string
	}{
		{
			name: "model matching rules",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models",
					Models:       []string{"gpt-4o"},
				},
			},
			want: "models must be empty",
		},
		{
			name: "converter",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models",
					Converter:    advancedCustomConverterOpenAIChatToOpenAIResponses,
				},
			},
			want: "converter must be none",
		},
		{
			name: "model placeholder",
			routes: []AdvancedCustomRoute{
				{
					IncomingPath: AdvancedCustomModelListPath,
					UpstreamPath: "/v1/models/{model}",
				},
			},
			want: "upstream_path must not contain {model}",
		},
		{
			name: "duplicate routes",
			routes: []AdvancedCustomRoute{
				{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/v1/models"},
				{IncomingPath: AdvancedCustomModelListPath, UpstreamPath: "/provider/models"},
			},
			want: "duplicates the /v1/models route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: tt.routes}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomModelListRouteRequiresExactIncomingPath(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/{model}",
				UpstreamPath: "/generic/{model}",
			},
			{
				IncomingPath: AdvancedCustomModelListPath,
				UpstreamPath: "/provider/models",
			},
		},
	}
	require.NoError(t, config.Validate())

	route, ok := config.ModelListRoute()
	require.True(t, ok)
	assert.Equal(t, "/provider/models", route.UpstreamPath)
}

func TestAdvancedCustomValidateBalanceRouteConstraints(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{{
			IncomingPath: AdvancedCustomBalancePath,
			UpstreamPath: "/provider/balance",
			Converter:    advancedCustomConverterNone,
		}},
	}
	require.NoError(t, valid.Validate())

	route, ok := valid.BalanceRoute()
	require.True(t, ok)
	assert.Equal(t, "/provider/balance", route.UpstreamPath)

	tests := []struct {
		name   string
		routes []AdvancedCustomRoute
		want   string
	}{
		{
			name: "model matching rules",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/balance",
				Models:       []string{"gpt-4o"},
			}},
			want: "models must be empty",
		},
		{
			name: "converter",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/balance",
				Converter:    advancedCustomConverterOpenAIChatToOpenAIResponses,
			}},
			want: "converter must be none",
		},
		{
			name: "model placeholder",
			routes: []AdvancedCustomRoute{{
				IncomingPath: AdvancedCustomBalancePath,
				UpstreamPath: "/provider/{model}/balance",
			}},
			want: "upstream_path must not contain {model}",
		},
		{
			name: "duplicate routes",
			routes: []AdvancedCustomRoute{
				{IncomingPath: AdvancedCustomBalancePath, UpstreamPath: "/provider/balance"},
				{IncomingPath: AdvancedCustomBalancePath, UpstreamPath: "/provider/credits"},
			},
			want: "duplicates the /v1/dashboard/billing/credit_grants route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: tt.routes}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomValidateDuplicateIncomingPathWithDisjointModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}

	require.NoError(t, config.Validate())
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsOverlappingModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"shared-model"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"shared-model"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "models overlaps")
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsMultipleCatchAllRoutes(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "catch-all already exists")
}

func TestAdvancedCustomValidateDuplicateIncomingPathRequiresCatchAllLast(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "catch-all route must be last")
}

func TestAdvancedCustomMatchPathForModel(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-2.5-flash"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	geminiRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, geminiRoute.Converter)

	chatRoute, ok := config.MatchPathForModel("/v1/responses", "gpt-4o")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToOpenAIChat, chatRoute.Converter)

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "unknown-model")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomMatchPathForModelRegexRules(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"re:(?i)^OAI-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	geminiRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, geminiRoute.Converter)

	chatRoute, ok := config.MatchPathForModel("/v1/responses", "oai-test")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToOpenAIChat, chatRoute.Converter)

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "gpt-4o")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomRouteModelRegexRulesAreCachedCompiled(t *testing.T) {
	require.True(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "cache-probe-model"))

	cached, ok := advancedCustomModelRegexCache.Load("^cache-probe-")
	require.True(t, ok)
	require.NotNil(t, cached)
	_, isRegexp := cached.(*regexp.Regexp)
	require.True(t, isRegexp)

	// Invalid patterns never match and are cached as nil so they are not recompiled.
	require.False(t, matchAdvancedCustomRouteModelRule("re:(", "anything"))
	cached, ok = advancedCustomModelRegexCache.Load("(")
	require.True(t, ok)
	re, _ := cached.(*regexp.Regexp)
	require.Nil(t, re)

	// Cached entries keep matching correctly on subsequent calls.
	require.True(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "cache-probe-other"))
	require.False(t, matchAdvancedCustomRouteModelRule("re:^cache-probe-", "other-model"))
}

func TestAdvancedCustomMatchPathForModelExactRuleDoesNotMatchPrefix(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/responses",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, config.Validate())

	fallbackRoute, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterNone, fallbackRoute.Converter)
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsInvalidRegexModels(t *testing.T) {
	tests := []struct {
		name   string
		models []string
		want   string
	}{
		{name: "empty regex", models: []string{"re:"}, want: "regex is empty"},
		{name: "invalid regex", models: []string{"re:["}, want: "regex is invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: "/v1/responses",
						UpstreamPath: "/v1beta/models/{model}:generateContent",
						Converter:    advancedCustomConverterOpenAIResponsesToGemini,
						Models:       tt.models,
					},
				},
			}

			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAdvancedCustomValidateDuplicateIncomingPathRejectsDuplicateRegexModels(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"re:^gemini-"},
			},
		},
	}

	err := config.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "models overlaps")
}

func TestAdvancedCustomMatchPathForModelUsesFirstMatchingRegexRoute(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gemini-2.5-flash"},
			},
		},
	}
	require.NoError(t, config.Validate())

	route, ok := config.MatchPathForModel("/v1/responses", "gemini-2.5-flash")
	require.True(t, ok)
	assert.Equal(t, advancedCustomConverterOpenAIResponsesToGemini, route.Converter)
}

func TestAdvancedCustomSupportedEndpointTypesForModel(t *testing.T) {
	config := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    advancedCustomConverterOpenAIResponsesToGemini,
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1beta/models/{model}:generateContent",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1beta/models/{model}:streamGenerateContent",
				UpstreamPath: "/v1beta/models/{model}:streamGenerateContent",
				Models:       []string{"re:^gemini-"},
			},
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/chat/completions",
				Models:       []string{"gpt-4o"},
			},
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/messages",
			},
			{
				IncomingPath: "/custom/endpoint",
				UpstreamPath: "/custom/endpoint",
			},
		},
	}
	require.NoError(t, config.Validate())

	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAIResponse,
		types.EndpointTypeGemini,
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("gemini-2.5-flash"))
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAI,
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("gpt-4o"))
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeAnthropic,
	}, config.SupportedEndpointTypesForModel("other-model"))
}

func TestAdvancedCustomValidateAlphaSearchConverterPath(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath: "/v1/alpha/search",
				UpstreamPath: "/v1/alpha/search",
				Converter:    advancedCustomConverterNone,
			},
		},
	}
	require.NoError(t, valid.Validate())
	assert.Equal(t, []types.EndpointType{
		types.EndpointTypeOpenAIAlphaSearch,
	}, valid.SupportedEndpointTypesForModel("gpt-5.1"))

	nonNoneConverters := []string{
		advancedCustomConverterClaudeMessagesToOpenAIChat,
		advancedCustomConverterOpenAIChatToClaudeMessages,
		advancedCustomConverterOpenAIChatToOpenAIResponses,
		advancedCustomConverterOpenAIResponsesToOpenAIChat,
		advancedCustomConverterOpenAIResponsesToGemini,
		advancedCustomConverterGeminiContentToOpenAIChat,
		advancedCustomConverterOpenAIChatToGeminiContent,
	}
	for _, converter := range nonNoneConverters {
		t.Run(converter, func(t *testing.T) {
			config := &AdvancedCustomConfig{
				Routes: []AdvancedCustomRoute{
					{
						IncomingPath: "/v1/alpha/search",
						UpstreamPath: "/v1/alpha/search",
						Converter:    converter,
					},
				},
			}
			err := config.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "converter does not match incoming_path")
		})
	}
}

func TestAdvancedCustomValidateRoutePassThroughBody(t *testing.T) {
	valid := &AdvancedCustomConfig{
		Routes: []AdvancedCustomRoute{
			{
				IncomingPath:           "/v1/chat/completions",
				UpstreamPath:           "/v1/chat/completions",
				PassThroughBodyEnabled: true,
			},
			{
				IncomingPath:           "/v1/rerank",
				UpstreamPath:           "/v1/rerank",
				Converter:              AdvancedCustomConverterSGLangRerank,
				PassThroughBodyEnabled: true,
			},
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/chat/completions",
				Converter:    advancedCustomConverterClaudeMessagesToOpenAIChat,
			},
		},
	}
	require.NoError(t, valid.Validate())

	route, ok := valid.MatchPathForModel("/v1/chat/completions", "gpt-4o")
	require.True(t, ok)
	assert.True(t, route.PassThroughBodyEnabled)
	route, ok = valid.MatchPathForModel("/v1/messages", "gpt-4o")
	require.True(t, ok)
	assert.False(t, route.PassThroughBodyEnabled)

	encoded, err := json.Marshal(valid.Routes[2])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "pass_through_body_enabled")

	tests := []struct {
		name  string
		route AdvancedCustomRoute
		want  string
	}{
		{
			name: "converter route",
			route: AdvancedCustomRoute{
				IncomingPath:           "/v1/messages",
				UpstreamPath:           "/v1/chat/completions",
				Converter:              advancedCustomConverterClaudeMessagesToOpenAIChat,
				PassThroughBodyEnabled: true,
			},
			want: "pass_through_body_enabled requires converter none",
		},
		{
			name: "model list route",
			route: AdvancedCustomRoute{
				IncomingPath:           AdvancedCustomModelListPath,
				UpstreamPath:           "/v1/models",
				PassThroughBodyEnabled: true,
			},
			want: "pass_through_body_enabled must be false for /v1/models",
		},
		{
			name: "balance route",
			route: AdvancedCustomRoute{
				IncomingPath:           AdvancedCustomBalancePath,
				UpstreamPath:           "/provider/balance",
				PassThroughBodyEnabled: true,
			},
			want: "pass_through_body_enabled must be false for /v1/dashboard/billing/credit_grants",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: []AdvancedCustomRoute{tt.route}}).Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestChannelSettingsHTTPTransportJSONRoundTrip(t *testing.T) {
	legacy := `{"proxy":"http://127.0.0.1:8080","force_format":true}`
	var settings ChannelSettings
	require.NoError(t, json.Unmarshal([]byte(legacy), &settings))
	assert.Equal(t, "http://127.0.0.1:8080", settings.Proxy)
	assert.True(t, settings.ForceFormat)
	assert.Empty(t, settings.HTTPProtocol)
	assert.Zero(t, settings.HTTP2ConnectionShards)

	encoded, err := json.Marshal(settings)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "http_protocol")
	assert.NotContains(t, string(encoded), "http2_connection_shards")
	assert.NotContains(t, string(encoded), "tls_insecure_skip_verify")

	explicit := ChannelSettings{
		Proxy:                 "socks5://127.0.0.1:1080",
		HTTPProtocol:          HTTPProtocolHTTP1,
		HTTP2ConnectionShards: 1,
		TLSInsecureSkipVerify: true,
	}
	encoded, err = json.Marshal(explicit)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"http_protocol":"http1"`)
	assert.Contains(t, string(encoded), `"tls_insecure_skip_verify":true`)

	var decoded ChannelSettings
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, explicit.HTTPProtocol, decoded.HTTPProtocol)
	assert.Equal(t, 1, decoded.HTTP2ConnectionShards)
	assert.True(t, decoded.TLSInsecureSkipVerify)

	sharded := ChannelSettings{HTTP2ConnectionShards: 4}
	encoded, err = json.Marshal(sharded)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"http2_connection_shards":4`)
	assert.NotContains(t, string(encoded), "http_protocol")
}

func TestChannelSettingsValidateHTTPTransport(t *testing.T) {
	require.NoError(t, (&ChannelSettings{}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTPProtocol: "AUTO"}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTPProtocol: "http1"}).ValidateHTTPTransport())
	require.NoError(t, (&ChannelSettings{HTTP2ConnectionShards: 8}).ValidateHTTPTransport())

	err := (&ChannelSettings{HTTPProtocol: "http2"}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http_protocol")

	err = (&ChannelSettings{HTTP2ConnectionShards: -1}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")

	err = (&ChannelSettings{HTTP2ConnectionShards: 9}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")

	err = (&ChannelSettings{HTTPProtocol: "http1", HTTP2ConnectionShards: 2}).ValidateHTTPTransport()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http2_connection_shards")
}

func TestChannelOtherSettingsValidateToolLossPolicy(t *testing.T) {
	require.NoError(t, (*ChannelOtherSettings)(nil).ValidateToolLossPolicy())
	require.NoError(t, (&ChannelOtherSettings{}).ValidateToolLossPolicy())
	require.NoError(t, (&ChannelOtherSettings{ToolLossPolicy: "allow"}).ValidateToolLossPolicy())
	require.NoError(t, (&ChannelOtherSettings{ToolLossPolicy: "safe"}).ValidateToolLossPolicy())
	require.NoError(t, (&ChannelOtherSettings{ToolLossPolicy: "strict"}).ValidateToolLossPolicy())

	err := (&ChannelOtherSettings{ToolLossPolicy: "drop"}).ValidateToolLossPolicy()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tool_loss_policy")
}
