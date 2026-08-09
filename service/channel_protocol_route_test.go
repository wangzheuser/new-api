package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
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
