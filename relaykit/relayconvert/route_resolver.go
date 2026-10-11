package relayconvert

import "github.com/QuantumNous/new-api/types"

// TextRoute describes a complete request and response conversion route.
type TextRoute struct {
	From              types.RelayFormat
	To                types.RelayFormat
	RequestConverter  string
	ResponseConverter string
	Quality           TextConverterQuality
	RequestSteps      []RequestStep
	ResponseSteps     []ResponseStep
	Stream            bool
}

// ResolveRoute resolves a text conversion route without executing it.
func ResolveRoute(from types.RelayFormat, to types.RelayFormat, stream bool) (TextRoute, bool) {
	if from == "" || to == "" || from == to {
		return TextRoute{}, false
	}

	requestSpec, ok := lookupRequestRoute(from, to)
	if !ok {
		return TextRoute{}, false
	}
	requestSpecs, err := expandRequestConverterSteps(requestSpec)
	if err != nil {
		return TextRoute{}, false
	}

	// Upstream responses travel in the reverse direction of requests.
	responseSpec, ok := lookupResponseRoute(to, from)
	if !ok {
		return TextRoute{}, false
	}
	responseSpecs, err := expandResponseConverterSteps(responseSpec)
	if err != nil || !responseStepsSupportMode(responseSpecs, stream) {
		return TextRoute{}, false
	}

	quality := worseTextConverterQuality(
		TextConverterQuality(requestSpec.Quality),
		TextConverterQuality(responseSpec.Quality),
	)
	requestSteps := make([]RequestStep, 0, len(requestSpecs))
	for _, spec := range requestSpecs {
		requestSteps = append(requestSteps, RequestStep{
			Converter: spec.ID,
			From:      spec.From,
			To:        spec.To,
		})
	}
	responseSteps := make([]ResponseStep, 0, len(responseSpecs))
	for _, spec := range responseSpecs {
		responseSteps = append(responseSteps, ResponseStep{
			Converter: spec.ID,
			From:      spec.From,
			To:        spec.To,
		})
	}

	return TextRoute{
		From:              from,
		To:                to,
		RequestConverter:  requestSpec.ID,
		ResponseConverter: responseSpec.ID,
		Quality:           quality,
		RequestSteps:      requestSteps,
		ResponseSteps:     responseSteps,
		Stream:            stream,
	}, true
}

func responseStepsSupportMode(specs []ResponseConverterSpec, stream bool) bool {
	for _, spec := range specs {
		if stream {
			if spec.ConvertStream == nil && spec.ConvertStreamChunk == nil {
				return false
			}
			continue
		}
		if spec.Convert == nil {
			return false
		}
	}
	return true
}

func worseTextConverterQuality(left TextConverterQuality, right TextConverterQuality) TextConverterQuality {
	if textConverterQualityRank(right) > textConverterQualityRank(left) {
		return right
	}
	return left
}

func textConverterQualityRank(quality TextConverterQuality) int {
	switch quality {
	case TextConverterQualityGood:
		return 1
	case TextConverterQualityFair:
		return 2
	case TextConverterQualityDiscouraged:
		return 3
	default:
		return 4
	}
}
