package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/relaynormalize"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProtocolProbeCasePreservesBasicCompatibility(t *testing.T) {
	probeCase, err := parseProtocolProbeCase("", true)
	require.NoError(t, err)
	assert.Equal(t, protocolProbeCaseBasic, probeCase)
	assert.Equal(t, "endpoint", probeCase.CapabilityLevel())

	probeCase, err = parseProtocolProbeCase("tool_cycle", true)
	require.NoError(t, err)
	assert.True(t, probeCase.IsSemantic())
	assert.Equal(t, "semantic", probeCase.CapabilityLevel())

	_, err = parseProtocolProbeCase("unknown", true)
	require.ErrorContains(t, err, "unsupported protocol probe case")
}

func TestResolveProtocolProbeExecutionUsesSavedModeOrCompatibilityDefault(t *testing.T) {
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI}
	channel.SetSetting(dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
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
	}})

	basic := resolveProtocolProbeExecution(channel, "MODEL_X", constant.EndpointTypeAnthropic, false, protocolProbeCaseBasic)
	assert.Equal(t, types.ChannelRouteModeNative, basic.RouteMode)
	assert.Empty(t, basic.RequestNormalizer)

	configured := resolveProtocolProbeExecution(channel, "MODEL_X", constant.EndpointTypeAnthropic, true, protocolProbeCaseReasoningHistory)
	assert.Equal(t, types.ChannelRouteModeNormalized, configured.RouteMode)
	assert.Equal(t, relaynormalize.RequestNormalizerAnthropicMessagesCompatible, configured.RequestNormalizer)
	assert.Equal(t, types.ReasoningHistoryPolicyStrip, configured.NormalizationOptions.ReasoningHistoryPolicy)

	synthetic := resolveProtocolProbeExecution(channel, "MODEL_Y", constant.EndpointTypeOpenAIResponse, false, protocolProbeCaseAssistantHistory)
	assert.Equal(t, types.ChannelRouteModeNormalized, synthetic.RouteMode)
	assert.Equal(t, relaynormalize.RequestNormalizerOpenAIResponsesCompatible, synthetic.RequestNormalizer)

	nativeOnly := resolveProtocolProbeExecution(channel, "MODEL_Y", constant.EndpointTypeOpenAI, false, protocolProbeCaseToolCycle)
	assert.Equal(t, types.ChannelRouteModeNative, nativeOnly.RouteMode)
	assert.Empty(t, nativeOnly.RequestNormalizer)
}

func TestBuildProtocolProbeRequestCoversEveryScenarioAndTextEndpoint(t *testing.T) {
	probeCases := []protocolProbeCase{
		protocolProbeCaseBasic,
		protocolProbeCaseAssistantHistory,
		protocolProbeCaseToolCycle,
		protocolProbeCaseReasoningHistory,
		protocolProbeCaseInvalidToolID,
		protocolProbeCaseToolIDCollision,
	}
	endpointTypes := []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini,
	}

	for _, endpointType := range endpointTypes {
		for _, probeCase := range probeCases {
			for _, stream := range []bool{false, true} {
				name := string(endpointType) + "/" + string(probeCase)
				if stream {
					name += "/stream"
				}
				t.Run(name, func(t *testing.T) {
					request, err := buildProtocolProbeRequest("MODEL_X", endpointType, channelTestOptions{
						isStream: stream, protocolProbeCase: probeCase,
					})
					require.NoError(t, err)
					require.NotNil(t, request)
					body, err := common.Marshal(request)
					require.NoError(t, err)
					assert.NotEmpty(t, body)
				})
			}
		}
	}
}

func TestSemanticProtocolProbeUsesLiveFinalWireNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name              string
		endpointType      constant.EndpointType
		relayFormat       types.RelayFormat
		probeCase         protocolProbeCase
		wantNormalized    int
		wantCollisions    int
		wantReasoningDrop int
		wantEmptyDrop     int
	}{
		{
			name: "anthropic reasoning history", endpointType: constant.EndpointTypeAnthropic,
			relayFormat: types.RelayFormatClaude, probeCase: protocolProbeCaseReasoningHistory,
			wantReasoningDrop: 1,
		},
		{
			name: "anthropic tool id collision", endpointType: constant.EndpointTypeAnthropic,
			relayFormat: types.RelayFormatClaude, probeCase: protocolProbeCaseToolIDCollision,
			wantNormalized: 4, wantCollisions: 1,
		},
		{
			name: "responses empty assistant", endpointType: constant.EndpointTypeOpenAIResponse,
			relayFormat: types.RelayFormatOpenAIResponses, probeCase: protocolProbeCaseAssistantHistory,
			wantEmptyDrop: 1,
		},
		{
			name: "responses tool id collision", endpointType: constant.EndpointTypeOpenAIResponse,
			relayFormat: types.RelayFormatOpenAIResponses, probeCase: protocolProbeCaseToolIDCollision,
			wantNormalized: 4, wantCollisions: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution := resolveProtocolProbeExecution(nil, "MODEL_X", test.endpointType, false, test.probeCase)
			options := channelTestOptions{
				protocolProbeCase: test.probeCase, protocolProbeExecution: execution,
			}
			request, err := buildProtocolProbeRequest("MODEL_X", test.endpointType, options)
			require.NoError(t, err)

			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{},
				ChannelRoutePlan: protocolProbeRoutePlan(
					test.endpointType, test.relayFormat, "/probe", 0, options,
				),
			}
			body, apiErr := relay.PrepareTextRouteRequestBody(ctx, info, request)
			require.Nil(t, apiErr)
			require.NotEmpty(t, body)
			require.NotNil(t, info.ProtocolNormalization)
			assert.Equal(t, test.wantNormalized, info.ProtocolNormalization.ToolIDsNormalized)
			assert.Equal(t, test.wantCollisions, info.ProtocolNormalization.ToolIDCollisions)
			assert.Equal(t, test.wantReasoningDrop, info.ProtocolNormalization.ReasoningOnlyAssistantDropped)
			assert.Equal(t, test.wantEmptyDrop, info.ProtocolNormalization.EmptyAssistantMessagesDropped)
		})
	}
}

func TestRecommendedProtocolProbeModeNeverPromotesBasicReachabilityToNative(t *testing.T) {
	execution := protocolProbeExecution{RouteMode: types.ChannelRouteModeNative}
	assert.Equal(t, protocolProbeRecommendedNormalized, recommendedProtocolProbeMode(
		nil, "MODEL_X", constant.EndpointTypeAnthropic, false,
		protocolProbeCaseBasic, execution, "confirmed",
	))
	assert.Equal(t, protocolProbeRecommendedUnsupported, recommendedProtocolProbeMode(
		nil, "MODEL_X", constant.EndpointTypeOpenAI, false,
		protocolProbeCaseBasic, execution, "confirmed",
	))
	assert.Equal(t, protocolProbeRecommendedUnsupported, recommendedProtocolProbeMode(
		nil, "MODEL_X", constant.EndpointTypeAnthropic, false,
		protocolProbeCaseAssistantHistory, execution, "upstream_error",
	))
}

func TestBuildDraftNativeProbeChannelCredentialSelection(t *testing.T) {
	t.Run("new channel uses first non-empty draft key", func(t *testing.T) {
		baseURL := "https://draft.example.com"
		probeChannel, err := buildDraftNativeProbeChannel(nil, &model.Channel{
			Type:    constant.ChannelTypeOpenAI,
			Key:     "  \n draft-key \n second-key ",
			BaseURL: &baseURL,
			Models:  "MODEL_X",
		})

		require.NoError(t, err)
		assert.Equal(t, "draft-key", probeChannel.Key)
		assert.Equal(t, baseURL, probeChannel.GetBaseURL())
		assert.False(t, probeChannel.ChannelInfo.IsMultiKey)
	})

	t.Run("blank draft key reuses saved enabled-key state without sharing maps", func(t *testing.T) {
		savedBaseURL := "https://saved.example.com"
		draftBaseURL := "https://draft.example.com"
		savedChannel := &model.Channel{
			Id:      42,
			Type:    constant.ChannelTypeOpenAI,
			Key:     "disabled-key\nenabled-key",
			BaseURL: &savedBaseURL,
			Models:  "MODEL_X",
			ChannelInfo: model.ChannelInfo{
				IsMultiKey:           true,
				MultiKeySize:         2,
				MultiKeyStatusList:   map[int]int{0: common.ChannelStatusAutoDisabled},
				MultiKeyPollingIndex: 1,
				MultiKeyMode:         constant.MultiKeyModePolling,
			},
		}
		probeChannel, err := buildDraftNativeProbeChannel(savedChannel, &model.Channel{
			Type:    constant.ChannelTypeOpenAI,
			BaseURL: &draftBaseURL,
			Models:  "MODEL_X",
		})

		require.NoError(t, err)
		assert.Equal(t, savedChannel.Id, probeChannel.Id)
		assert.Equal(t, savedChannel.Key, probeChannel.Key)
		assert.Equal(t, draftBaseURL, probeChannel.GetBaseURL())
		assert.True(t, probeChannel.ChannelInfo.IsMultiKey)
		assert.Equal(t, common.ChannelStatusAutoDisabled, probeChannel.ChannelInfo.MultiKeyStatusList[0])
		assert.Equal(t, constant.MultiKeyModeRandom, probeChannel.ChannelInfo.MultiKeyMode)
		selectedKey, selectedIndex, apiErr := probeChannel.GetNextEnabledKey()
		require.Nil(t, apiErr)
		assert.Equal(t, "enabled-key", selectedKey)
		assert.Equal(t, 1, selectedIndex)
		assert.Equal(t, 1, savedChannel.ChannelInfo.MultiKeyPollingIndex)

		probeChannel.ChannelInfo.MultiKeyStatusList[0] = common.ChannelStatusEnabled
		assert.Equal(t, common.ChannelStatusAutoDisabled, savedChannel.ChannelInfo.MultiKeyStatusList[0])
	})

	t.Run("explicit edit key is isolated from saved multi-key metadata", func(t *testing.T) {
		savedChannel := &model.Channel{
			Id:  42,
			Key: "saved-key",
			ChannelInfo: model.ChannelInfo{
				IsMultiKey:         true,
				MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled},
			},
		}
		probeChannel, err := buildDraftNativeProbeChannel(savedChannel, &model.Channel{
			Type:   constant.ChannelTypeOpenAI,
			Key:    "draft-key",
			Models: "MODEL_X",
		})

		require.NoError(t, err)
		assert.Equal(t, "draft-key", probeChannel.Key)
		assert.False(t, probeChannel.ChannelInfo.IsMultiKey)
		assert.Empty(t, probeChannel.ChannelInfo.MultiKeyStatusList)
	})

	_, err := buildDraftNativeProbeChannel(nil, &model.Channel{Type: constant.ChannelTypeOpenAI})
	require.ErrorContains(t, err, "API key is required")
}

func TestCompactProbeErrorRedactsCredentialsBeforeTruncation(t *testing.T) {
	message := compactProbeError("request failed with draft-secret and saved-secret", "draft-secret", "saved-secret")

	assert.Equal(t, "request failed with *** and ***", message)
	assert.NotContains(t, message, "secret")
}
