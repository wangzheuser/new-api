package service

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/relaynormalize"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func protocolTestChannel(t *testing.T, settings dto.ChannelSettings) *model.Channel {
	t.Helper()
	raw, err := common.Marshal(settings)
	require.NoError(t, err)
	return &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Setting: common.GetPointer(string(raw))}
}

func TestPlanChannelProtocolRouteUsesNormalizedModelOverrideForClaude(t *testing.T) {
	channel := protocolTestChannel(t, dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
		Native: map[constant.EndpointType]dto.ProtocolCapability{
			constant.EndpointTypeOpenAI: {NonStream: true, Stream: true},
		},
		ModelOverrides: map[string]dto.ModelProtocolProfile{
			"MODEL_X": {Native: map[constant.EndpointType]dto.ProtocolCapability{
				constant.EndpointTypeAnthropic: {
					NonStream:        true,
					Stream:           true,
					Mode:             dto.ProtocolHandlingModeNormalized,
					ReasoningHistory: types.ReasoningHistoryPolicyStrip,
				},
			}},
		},
		AutoConvert: true,
		MaxQuality:  dto.ProtocolConversionQualityFair,
	}})

	for _, stream := range []bool{false, true} {
		plan, err := PlanChannelProtocolRoute(channel, "MODEL_X", "/v1/messages", stream)
		require.NoError(t, err)
		require.NotNil(t, plan)
		assert.Equal(t, types.ChannelRouteModeNormalized, plan.RouteMode)
		assert.Equal(t, relaynormalize.RequestNormalizerAnthropicMessagesCompatible, plan.RequestNormalizer)
		assert.Equal(t, types.ReasoningHistoryPolicyStrip, plan.NormalizationOptions.ReasoningHistoryPolicy)
		assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), plan.ClientRelayFormat)
		assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), plan.UpstreamRelayFormat)
		assert.Equal(t, "/v1/messages", plan.UpstreamPath)
		assert.Empty(t, plan.ResponseConverter)
		assert.Equal(t, "model_override", plan.CapabilitySource)
		assert.Equal(t, stream, plan.Stream)
	}

	native, err := PlanChannelProtocolRoute(channel, "MODEL_Y", "/v1/chat/completions", false)
	require.NoError(t, err)
	require.NotNil(t, native)
	assert.Equal(t, types.ChannelRouteModeNative, native.RouteMode)
	assert.Empty(t, native.RequestNormalizer)
}

func TestPlanChannelProtocolRouteUsesNormalizedModelOverrideForResponses(t *testing.T) {
	channel := protocolTestChannel(t, dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
		Native: map[constant.EndpointType]dto.ProtocolCapability{
			constant.EndpointTypeOpenAI: {NonStream: true, Stream: true},
		},
		ModelOverrides: map[string]dto.ModelProtocolProfile{
			"MODEL_X": {Native: map[constant.EndpointType]dto.ProtocolCapability{
				constant.EndpointTypeOpenAIResponse: {
					NonStream: true,
					Stream:    true,
					Mode:      dto.ProtocolHandlingModeNormalized,
				},
			}},
		},
		AutoConvert: true,
		MaxQuality:  dto.ProtocolConversionQualityFair,
	}})

	for _, stream := range []bool{false, true} {
		plan, err := PlanChannelProtocolRoute(channel, "MODEL_X", "/v1/responses", stream)
		require.NoError(t, err)
		require.NotNil(t, plan)
		assert.Equal(t, types.ChannelRouteModeNormalized, plan.RouteMode)
		assert.Equal(t, relaynormalize.RequestNormalizerOpenAIResponsesCompatible, plan.RequestNormalizer)
		assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), plan.ClientRelayFormat)
		assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), plan.UpstreamRelayFormat)
		assert.Equal(t, "/v1/responses", plan.UpstreamPath)
		assert.Empty(t, plan.ResponseConverter)
		assert.Equal(t, "model_override", plan.CapabilitySource)
		assert.Equal(t, stream, plan.Stream)
	}
}

func TestPlanChannelProtocolRouteNormalizesConvertedTargetProtocol(t *testing.T) {
	tests := []struct {
		name               string
		path               string
		target             constant.EndpointType
		expectedNormalizer string
	}{
		{
			name:               "anthropic target",
			path:               "/v1/chat/completions",
			target:             constant.EndpointTypeAnthropic,
			expectedNormalizer: relaynormalize.RequestNormalizerAnthropicMessagesCompatible,
		},
		{
			name:               "responses target",
			path:               "/v1/messages",
			target:             constant.EndpointTypeOpenAIResponse,
			expectedNormalizer: relaynormalize.RequestNormalizerOpenAIResponsesCompatible,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := protocolTestChannel(t, dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
				Native: map[constant.EndpointType]dto.ProtocolCapability{
					tt.target: {
						NonStream: true,
						Stream:    true,
						Mode:      dto.ProtocolHandlingModeNormalized,
					},
				},
				AutoConvert: true,
				MaxQuality:  dto.ProtocolConversionQualityFair,
			}})

			plan, err := PlanChannelProtocolRoute(channel, "MODEL_X", tt.path, false)
			require.NoError(t, err)
			require.NotNil(t, plan)
			assert.Equal(t, types.ChannelRouteModeConverted, plan.RouteMode)
			assert.Equal(t, tt.target, plan.UpstreamEndpointType)
			assert.Equal(t, tt.expectedNormalizer, plan.RequestNormalizer)
			if tt.target == constant.EndpointTypeAnthropic {
				assert.Equal(t, types.ReasoningHistoryPolicyPreserve, plan.NormalizationOptions.ReasoningHistoryPolicy)
			}
		})
	}
}

func TestPlanChannelProtocolRoutePrefersNativeAndModelOverride(t *testing.T) {
	channel := protocolTestChannel(t, dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
		Native: map[constant.EndpointType]dto.ProtocolCapability{
			constant.EndpointTypeOpenAI: {NonStream: true, Stream: true},
		},
		ModelOverrides: map[string]dto.ModelProtocolProfile{
			"MODEL_X": {Native: map[constant.EndpointType]dto.ProtocolCapability{
				constant.EndpointTypeOpenAIResponse: {NonStream: true, Stream: true},
			}},
		},
		AutoConvert: true,
		MaxQuality:  dto.ProtocolConversionQualityFair,
	}})

	plan, err := PlanChannelProtocolRoute(channel, "MODEL_Y", "/v1/chat/completions", false)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, types.ChannelRouteModeNative, plan.RouteMode)
	assert.Equal(t, "channel_default", plan.CapabilitySource)

	plan, err = PlanChannelProtocolRoute(channel, "MODEL_X", "/v1/chat/completions", false)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, types.ChannelRouteModeConverted, plan.RouteMode)
	assert.Equal(t, constant.EndpointTypeOpenAIResponse, plan.UpstreamEndpointType)
	assert.Equal(t, "good", plan.Quality)
	assert.Equal(t, "model_override", plan.CapabilitySource)
}

func TestPlanChannelProtocolRouteFiltersQualityStreamAndDiscouraged(t *testing.T) {
	tests := []struct {
		name       string
		settings   dto.ChannelSettings
		path       string
		stream     bool
		expected   constant.EndpointType
		expectPlan bool
	}{
		{
			name: "good ceiling excludes fair",
			settings: dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
				Native:      map[constant.EndpointType]dto.ProtocolCapability{constant.EndpointTypeAnthropic: {NonStream: true}},
				AutoConvert: true, MaxQuality: dto.ProtocolConversionQualityGood,
			}},
			path: "/v1/chat/completions",
		},
		{
			name: "discouraged direct route excluded",
			settings: dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
				Native:      map[constant.EndpointType]dto.ProtocolCapability{constant.EndpointTypeGemini: {NonStream: true}},
				AutoConvert: true, MaxQuality: dto.ProtocolConversionQualityFair,
			}},
			path: "/v1/messages",
		},
		{
			name: "stream filters non stream target",
			settings: dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
				Native:      map[constant.EndpointType]dto.ProtocolCapability{constant.EndpointTypeOpenAI: {NonStream: true}},
				AutoConvert: true, MaxQuality: dto.ProtocolConversionQualityFair,
			}},
			path: "/v1/responses", stream: true,
		},
		{
			name: "fixed target order",
			settings: dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
				Native: map[constant.EndpointType]dto.ProtocolCapability{
					constant.EndpointTypeAnthropic: {NonStream: true},
					constant.EndpointTypeGemini:    {NonStream: true},
				},
				AutoConvert: true, MaxQuality: dto.ProtocolConversionQualityFair,
			}},
			path: "/v1/chat/completions", expected: constant.EndpointTypeAnthropic, expectPlan: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanChannelProtocolRoute(protocolTestChannel(t, tt.settings), "MODEL_X", tt.path, tt.stream)
			require.NoError(t, err)
			if !tt.expectPlan {
				assert.Nil(t, plan)
				return
			}
			require.NotNil(t, plan)
			assert.Equal(t, tt.expected, plan.UpstreamEndpointType)
		})
	}
}

func TestPlanChannelProtocolRouteLegacyAndPassThrough(t *testing.T) {
	legacy, err := PlanChannelProtocolRoute(protocolTestChannel(t, dto.ChannelSettings{}), "MODEL_X", "/v1/responses", false)
	require.NoError(t, err)
	require.NotNil(t, legacy)
	assert.Equal(t, types.ChannelRouteModeLegacy, legacy.RouteMode)

	channel := protocolTestChannel(t, dto.ChannelSettings{
		PassThroughBodyEnabled: true,
		ProtocolPolicy: &dto.ChannelProtocolPolicy{
			Native:      map[constant.EndpointType]dto.ProtocolCapability{constant.EndpointTypeOpenAI: {NonStream: true}},
			AutoConvert: true, MaxQuality: dto.ProtocolConversionQualityFair,
		},
	})
	converted, err := PlanChannelProtocolRoute(channel, "MODEL_X", "/v1/responses", false)
	require.NoError(t, err)
	assert.Nil(t, converted)
	native, err := PlanChannelProtocolRoute(channel, "MODEL_X", "/v1/chat/completions", false)
	require.NoError(t, err)
	require.NotNil(t, native)
	assert.Equal(t, types.ChannelRouteModeNative, native.RouteMode)
}

func TestResolveClientTextProtocolExcludesNonGenerationSubpaths(t *testing.T) {
	for _, path := range []string{
		"/v1/messages/count_tokens",
		"/v1/responses/response_id",
		"/v1/responses/compact",
	} {
		_, _, _, ok := ResolveClientTextProtocol(path)
		assert.False(t, ok, path)
	}
}

func TestObservedProtocolRoutesKeepConfigurationAndCredentialsIsolated(t *testing.T) {
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	channel := protocolTestChannel(t, dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
		Native:      map[constant.EndpointType]dto.ProtocolCapability{constant.EndpointTypeOpenAI: {NonStream: true, Stream: true}, constant.EndpointTypeOpenAIResponse: {NonStream: true, Stream: true}},
		AutoConvert: true, RuntimeFallback: true,
	}})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	plan, key, err := PlanObservedProtocolRoute(c, channel, "model", "key-a", false)
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, constant.EndpointTypeOpenAI, plan.UpstreamEndpointType)
	MarkProtocolAttempt(c, key, plan.UpstreamEndpointType)
	RecordProtocolObservation(key, plan.UpstreamEndpointType, false)
	next, _, err := PlanObservedProtocolRoute(c, channel, "model", "key-a", false)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, constant.EndpointTypeOpenAIResponse, next.UpstreamEndpointType)
	assert.Equal(t, "protocol_fallback", next.SelectionReason)
	fresh, _ := gin.CreateTestContext(httptest.NewRecorder())
	fresh.Request = c.Request
	cached, _, err := PlanObservedProtocolRoute(fresh, channel, "model", "key-a", false)
	require.NoError(t, err)
	require.NotNil(t, cached)
	assert.Equal(t, constant.EndpointTypeOpenAIResponse, cached.UpstreamEndpointType)
	assert.Equal(t, "protocol_observation", cached.SelectionReason)
	for _, tc := range []struct {
		model, key string
		stream     bool
	}{{"model", "key-b", false}, {"model", "key-a", true}, {"other", "key-a", false}} {
		separate, _, err := PlanObservedProtocolRoute(fresh, channel, tc.model, tc.key, tc.stream)
		require.NoError(t, err)
		require.NotNil(t, separate)
		assert.Equal(t, constant.EndpointTypeOpenAI, separate.UpstreamEndpointType)
	}
	MarkProtocolAttempt(c, key, next.UpstreamEndpointType)
	exhausted, _, err := PlanObservedProtocolRoute(c, channel, "model", "key-a", false)
	require.NoError(t, err)
	assert.Nil(t, exhausted)
	require.NoError(t, ClearProtocolObservations(context.Background(), channel.Id))
	restored, _, err := PlanObservedProtocolRoute(fresh, channel, "model", "key-a", false)
	require.NoError(t, err)
	require.NotNil(t, restored)
	assert.Equal(t, constant.EndpointTypeOpenAI, restored.UpstreamEndpointType)

	original, err := common.Marshal(channel.GetSetting())
	require.NoError(t, err)
	channel.ModelMapping = common.GetPointer(`{"alias-a":"actual","alias-b":"actual"}`)
	assert.Equal(t, ProtocolObservationKey(channel, "alias-a", "key-a", false), ProtocolObservationKey(channel, "alias-b", "key-a", false))
	before := ProtocolObservationKey(channel, "alias-a", "key-a", false)
	channel.HeaderOverride = common.GetPointer(`{"X-Wire":"new"}`)
	assert.NotEqual(t, before, ProtocolObservationKey(channel, "alias-a", "key-a", false))
	channel.HeaderOverride = common.GetPointer(`{"X-Account":"{client_header:X-Account}"}`)
	assert.NotEqual(t, ProtocolObservationKey(channel, "alias-a", "key-a", false, http.Header{"X-Account": {"one"}}), ProtocolObservationKey(channel, "alias-a", "key-a", false, http.Header{"X-Account": {"two"}}))
	after, err := common.Marshal(channel.GetSetting())
	require.NoError(t, err)
	assert.Equal(t, string(original), string(after), "observations never rewrite static capability configuration")
	settings := channel.GetSetting()
	settings.ProtocolPolicy.RuntimeFallback = false
	channel.SetSetting(settings)
	disabled, disabledKey, err := PlanObservedProtocolRoute(fresh, channel, "model", "key-a", false)
	require.NoError(t, err)
	assert.Nil(t, disabled)
	assert.Empty(t, disabledKey)
	settings.ProtocolPolicy.RuntimeFallback = true
	settings.PassThroughBodyEnabled = true
	channel.SetSetting(settings)
	passthrough, _, err := PlanObservedProtocolRoute(fresh, channel, "model", "key-a", false)
	require.NoError(t, err)
	assert.Nil(t, passthrough)
	settings.PassThroughBodyEnabled = false
	channel.SetSetting(settings)
	fresh.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	nonText, nonTextKey, err := PlanObservedProtocolRoute(fresh, channel, "model", "key-a", false)
	require.NoError(t, err)
	assert.Nil(t, nonText)
	assert.Empty(t, nonTextKey)
	for _, body := range []string{`{"previous_response_id":"resp_1"}`, `{"input":[{"encrypted_content":"sealed"}]}`, `{"conversation":{"id":"one"}}`} {
		var request any
		require.NoError(t, common.UnmarshalJsonStr(body, &request))
		assert.False(t, ProtocolRequestReplayable(request))
	}
	assert.True(t, ProtocolRequestReplayable(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}}))
	for _, tc := range []struct {
		status  int
		message string
		want    bool
	}{
		{400, "This model is only supported on /v1/responses", true},
		{404, "Model not supported on this endpoint", true},
		{404, "model does not exist", false}, {401, "unsupported_endpoint", false},
		{429, "unsupported_endpoint", false}, {503, "unsupported_endpoint", false},
		{400, "tools not supported on this endpoint", false},
	} {
		apiErr := types.NewErrorWithStatusCode(fmt.Errorf("%s", tc.message), types.ErrorCodeBadResponseStatusCode, tc.status, types.ErrOptionWithUpstreamStatusCode(tc.status))
		assert.Equal(t, tc.want, ProtocolEndpointRefused(apiErr), tc.message)
	}
}

func TestProtocolObservationRedisIsolationAndReset(t *testing.T) {
	addr := os.Getenv("TEST_PROTOCOL_REDIS")
	if addr == "" {
		t.Skip("TEST_PROTOCOL_REDIS not configured")
	}
	oldRedis, oldRDB := common.RedisEnabled, common.RDB
	client := redis.NewClient(&redis.Options{Addr: addr})
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() { common.RedisEnabled, common.RDB = oldRedis, oldRDB; require.NoError(t, client.Close()) })
	require.NoError(t, client.Ping(context.Background()).Err())
	key := "newapi:protocol-observation:v1:900001:test"
	other := "newapi:protocol-observation:v1:900002:test"
	RecordProtocolObservation(key, constant.EndpointTypeOpenAI, false)
	RecordProtocolObservation(other, constant.EndpointTypeOpenAI, true)
	observation, found := readProtocolObservation(key + ":openai")
	require.True(t, found)
	assert.False(t, observation.Supported)
	ttl, err := client.TTL(context.Background(), key+":openai").Result()
	require.NoError(t, err)
	assert.Positive(t, ttl)
	require.NoError(t, ClearProtocolObservations(context.Background(), 900001))
	_, found = readProtocolObservation(key + ":openai")
	assert.False(t, found)
	observation, found = readProtocolObservation(other + ":openai")
	require.True(t, found)
	assert.True(t, observation.Supported)
	require.NoError(t, ClearProtocolObservations(context.Background(), 900002))
}
