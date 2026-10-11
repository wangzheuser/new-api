package relayconvert

import (
	kitconvert "github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type TextRoute struct {
	RequestConverter  string
	ResponseConverter string
	Quality           TextConverterQuality
	RequestSteps      []string
	ResponseSteps     []string
}

func ResolveRoute(from, to types.RelayFormat, stream bool) (TextRoute, bool) {
	route, ok := kitconvert.ResolveRoute(from, to, stream)
	if !ok {
		return TextRoute{}, false
	}
	requestSteps := make([]string, 0, len(route.RequestSteps))
	for _, step := range route.RequestSteps {
		requestSteps = append(requestSteps, step.Converter)
	}
	responseSteps := make([]string, 0, len(route.ResponseSteps))
	for _, step := range route.ResponseSteps {
		responseSteps = append(responseSteps, step.Converter)
	}
	return TextRoute{
		RequestConverter:  route.RequestConverter,
		ResponseConverter: route.ResponseConverter,
		Quality:           TextConverterQuality(route.Quality),
		RequestSteps:      requestSteps,
		ResponseSteps:     responseSteps,
	}, true
}
