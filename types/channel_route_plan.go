package types

import "github.com/QuantumNous/new-api/constant"

type ChannelRouteMode string

const (
	ChannelRouteModeLegacy     ChannelRouteMode = "legacy"
	ChannelRouteModeNative     ChannelRouteMode = "native"
	ChannelRouteModeNormalized ChannelRouteMode = "normalized"
	ChannelRouteModeConverted  ChannelRouteMode = "converted"
)

// ChannelRoutePlan freezes the protocol route selected for one channel attempt.
type ChannelRoutePlan struct {
	ClientEndpointType   constant.EndpointType       `json:"client_endpoint_type"`
	UpstreamEndpointType constant.EndpointType       `json:"upstream_endpoint_type"`
	ClientRelayFormat    RelayFormat                 `json:"client_relay_format"`
	UpstreamRelayFormat  RelayFormat                 `json:"upstream_relay_format"`
	ClientRelayMode      int                         `json:"client_relay_mode"`
	UpstreamRelayMode    int                         `json:"upstream_relay_mode"`
	ClientPath           string                      `json:"client_path"`
	UpstreamPath         string                      `json:"upstream_path"`
	RouteMode            ChannelRouteMode            `json:"route_mode"`
	RequestConverter     string                      `json:"request_converter,omitempty"`
	RequestNormalizer    string                      `json:"request_normalizer,omitempty"`
	NormalizationOptions RequestNormalizationOptions `json:"normalization_options,omitempty"`
	ResponseConverter    string                      `json:"response_converter,omitempty"`
	Quality              string                      `json:"quality,omitempty"`
	RequestSteps         int                         `json:"request_steps"`
	ResponseSteps        int                         `json:"response_steps"`
	Stream               bool                        `json:"stream"`
	CapabilitySource     string                      `json:"capability_source,omitempty"`
}
