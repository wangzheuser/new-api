package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/service/relaynormalize"
	"github.com/QuantumNous/new-api/types"
)

type textProtocolDescriptor struct {
	EndpointType constant.EndpointType
	RelayFormat  types.RelayFormat
	RelayMode    int
	Path         string
	Rank         int
}

type protocolRouteCandidate struct {
	descriptor textProtocolDescriptor
	route      relayconvert.TextRoute
}

var textProtocolDescriptors = []textProtocolDescriptor{
	{
		EndpointType: constant.EndpointTypeOpenAI,
		RelayFormat:  types.RelayFormatOpenAI,
		RelayMode:    relayconstant.RelayModeChatCompletions,
		Path:         "/v1/chat/completions",
		Rank:         0,
	},
	{
		EndpointType: constant.EndpointTypeOpenAIResponse,
		RelayFormat:  types.RelayFormatOpenAIResponses,
		RelayMode:    relayconstant.RelayModeResponses,
		Path:         "/v1/responses",
		Rank:         1,
	},
	{
		EndpointType: constant.EndpointTypeAnthropic,
		RelayFormat:  types.RelayFormatClaude,
		RelayMode:    relayconstant.RelayModeUnknown,
		Path:         "/v1/messages",
		Rank:         2,
	},
	{
		EndpointType: constant.EndpointTypeGemini,
		RelayFormat:  types.RelayFormatGemini,
		RelayMode:    relayconstant.RelayModeGemini,
		Path:         "/v1beta/models/{model}:generateContent",
		Rank:         3,
	},
}

// ResolveClientTextProtocol resolves an incoming path to one of the four text protocols.
func ResolveClientTextProtocol(path string) (constant.EndpointType, types.RelayFormat, int, bool) {
	path = strings.TrimSuffix(strings.SplitN(path, "?", 2)[0], "/")
	switch {
	case path == "/v1/chat/completions", path == "/pg/chat/completions":
		return constant.EndpointTypeOpenAI, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, true
	case path == "/v1/responses":
		return constant.EndpointTypeOpenAIResponse, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true
	case path == "/v1/messages":
		return constant.EndpointTypeAnthropic, types.RelayFormatClaude, relayconstant.RelayModeUnknown, true
	case strings.HasPrefix(path, "/v1beta/models/"), strings.HasPrefix(path, "/v1/models/"):
		if strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent") {
			return constant.EndpointTypeGemini, types.RelayFormatGemini, relayconstant.RelayModeGemini, true
		}
	}
	return "", "", relayconstant.RelayModeUnknown, false
}

// PlanChannelProtocolRoute creates the immutable protocol plan for one channel attempt.
func PlanChannelProtocolRoute(channel *model.Channel, modelName string, clientPath string, stream bool) (*types.ChannelRoutePlan, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is required")
	}
	clientEndpoint, clientFormat, clientMode, ok := ResolveClientTextProtocol(clientPath)
	if !ok {
		return nil, nil
	}

	settings := channel.GetSetting()
	policy := settings.ProtocolPolicy
	if policy == nil || channel.Type == constant.ChannelTypeAdvancedCustom {
		return newUnconvertedRoutePlan(clientEndpoint, clientFormat, clientMode, clientPath, stream, types.ChannelRouteModeLegacy, "legacy"), nil
	}
	apiType, _ := common.ChannelType2APIType(channel.Type)
	if apiType != constant.APITypeOpenAI {
		return nil, nil
	}

	native, source := policy.NativeForModel(modelName)
	clientCapability := native[clientEndpoint]
	if protocolCapabilitySupports(clientCapability, stream) {
		routeMode := types.ChannelRouteModeNative
		requestNormalizer := ""
		switch clientCapability.EffectiveMode() {
		case dto.ProtocolHandlingModeNative:
		case dto.ProtocolHandlingModeNormalized:
			if settings.PassThroughBodyEnabled {
				return nil, fmt.Errorf("normalized protocol handling conflicts with request body pass-through")
			}
			routeMode = types.ChannelRouteModeNormalized
			var supported bool
			requestNormalizer, supported = requestNormalizerForEndpoint(clientEndpoint)
			if !supported {
				return nil, fmt.Errorf("normalized protocol handling is unsupported for %s", clientEndpoint)
			}
		default:
			return nil, fmt.Errorf("invalid protocol handling mode: %s", clientCapability.Mode)
		}
		plan := newUnconvertedRoutePlan(clientEndpoint, clientFormat, clientMode, clientPath, stream, routeMode, source)
		plan.RequestNormalizer = requestNormalizer
		if requestNormalizer != "" {
			plan.NormalizationOptions = normalizationOptionsForCapability(clientEndpoint, clientCapability)
		}
		return plan, nil
	}
	if !policy.AutoConvert || settings.PassThroughBodyEnabled {
		return nil, nil
	}

	candidates := make([]protocolRouteCandidate, 0, len(textProtocolDescriptors))
	for _, descriptor := range textProtocolDescriptors {
		if !protocolCapabilitySupports(native[descriptor.EndpointType], stream) {
			continue
		}
		route, exists := relayconvert.ResolveRoute(clientFormat, descriptor.RelayFormat, stream)
		if !exists || !protocolQualityAllowed(route.Quality, policy.EffectiveMaxQuality()) {
			continue
		}
		candidates = append(candidates, protocolRouteCandidate{descriptor: descriptor, route: route})
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if protocolQualityRank(left.route.Quality) != protocolQualityRank(right.route.Quality) {
			return protocolQualityRank(left.route.Quality) < protocolQualityRank(right.route.Quality)
		}
		leftSteps := len(left.route.RequestSteps) + len(left.route.ResponseSteps)
		rightSteps := len(right.route.RequestSteps) + len(right.route.ResponseSteps)
		if leftSteps != rightSteps {
			return leftSteps < rightSteps
		}
		return left.descriptor.Rank < right.descriptor.Rank
	})

	selected := candidates[0]
	requestNormalizer := ""
	selectedCapability := native[selected.descriptor.EndpointType]
	switch selectedCapability.EffectiveMode() {
	case dto.ProtocolHandlingModeNative:
	case dto.ProtocolHandlingModeNormalized:
		var supported bool
		requestNormalizer, supported = requestNormalizerForEndpoint(selected.descriptor.EndpointType)
		if !supported {
			return nil, fmt.Errorf("normalized protocol handling is unsupported for %s", selected.descriptor.EndpointType)
		}
	default:
		return nil, fmt.Errorf("invalid protocol handling mode: %s", selectedCapability.Mode)
	}
	plan := &types.ChannelRoutePlan{
		ClientEndpointType:   clientEndpoint,
		UpstreamEndpointType: selected.descriptor.EndpointType,
		ClientRelayFormat:    clientFormat,
		UpstreamRelayFormat:  selected.descriptor.RelayFormat,
		ClientRelayMode:      clientMode,
		UpstreamRelayMode:    selected.descriptor.RelayMode,
		ClientPath:           clientPath,
		UpstreamPath:         protocolPath(selected.descriptor, modelName, stream),
		RouteMode:            types.ChannelRouteModeConverted,
		RequestConverter:     selected.route.RequestConverter,
		RequestNormalizer:    requestNormalizer,
		ResponseConverter:    selected.route.ResponseConverter,
		Quality:              string(selected.route.Quality),
		RequestSteps:         len(selected.route.RequestSteps),
		ResponseSteps:        len(selected.route.ResponseSteps),
		Stream:               stream,
		CapabilitySource:     source,
	}
	if requestNormalizer != "" {
		plan.NormalizationOptions = normalizationOptionsForCapability(selected.descriptor.EndpointType, selectedCapability)
	}
	return plan, nil
}

// normalizationOptionsForCapability resolves final-wire behavior owned by one normalized endpoint.
func normalizationOptionsForCapability(endpoint constant.EndpointType, capability dto.ProtocolCapability) types.RequestNormalizationOptions {
	options := types.RequestNormalizationOptions{}
	if endpoint == constant.EndpointTypeAnthropic {
		options.ReasoningHistoryPolicy = capability.EffectiveReasoningHistoryPolicy()
	}
	return options
}

// requestNormalizerForEndpoint resolves the final-wire normalizer registered for one protocol endpoint.
func requestNormalizerForEndpoint(endpoint constant.EndpointType) (string, bool) {
	switch endpoint {
	case constant.EndpointTypeAnthropic:
		return relaynormalize.RequestNormalizerAnthropicMessagesCompatible, true
	case constant.EndpointTypeOpenAIResponse:
		return relaynormalize.RequestNormalizerOpenAIResponsesCompatible, true
	default:
		return "", false
	}
}

func newUnconvertedRoutePlan(endpoint constant.EndpointType, format types.RelayFormat, relayMode int, path string, stream bool, routeMode types.ChannelRouteMode, source string) *types.ChannelRoutePlan {
	return &types.ChannelRoutePlan{
		ClientEndpointType:   endpoint,
		UpstreamEndpointType: endpoint,
		ClientRelayFormat:    format,
		UpstreamRelayFormat:  format,
		ClientRelayMode:      relayMode,
		UpstreamRelayMode:    relayMode,
		ClientPath:           path,
		UpstreamPath:         path,
		RouteMode:            routeMode,
		Stream:               stream,
		CapabilitySource:     source,
	}
}

func protocolCapabilitySupports(capability dto.ProtocolCapability, stream bool) bool {
	if stream {
		return capability.Stream
	}
	return capability.NonStream
}

func protocolPath(descriptor textProtocolDescriptor, modelName string, stream bool) string {
	path := strings.ReplaceAll(descriptor.Path, "{model}", modelName)
	if descriptor.EndpointType == constant.EndpointTypeGemini && stream {
		return strings.Replace(path, ":generateContent", ":streamGenerateContent?alt=sse", 1)
	}
	return path
}

// BuildTextProtocolPath returns the upstream path for a text endpoint and mapped model.
func BuildTextProtocolPath(endpointType constant.EndpointType, modelName string, stream bool) (string, bool) {
	for _, descriptor := range textProtocolDescriptors {
		if descriptor.EndpointType == endpointType {
			return protocolPath(descriptor, modelName, stream), true
		}
	}
	return "", false
}

func protocolQualityAllowed(routeQuality relayconvert.TextConverterQuality, maxQuality dto.ProtocolConversionQuality) bool {
	if routeQuality == relayconvert.TextConverterQualityDiscouraged {
		return false
	}
	return protocolQualityRank(routeQuality) <= protocolMaxQualityRank(maxQuality)
}

func protocolQualityRank(quality relayconvert.TextConverterQuality) int {
	switch quality {
	case relayconvert.TextConverterQualityGood:
		return 1
	case relayconvert.TextConverterQualityFair:
		return 2
	default:
		return 3
	}
}

func protocolMaxQualityRank(quality dto.ProtocolConversionQuality) int {
	if quality == dto.ProtocolConversionQualityGood {
		return 1
	}
	return 2
}
