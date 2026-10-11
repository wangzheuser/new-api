package relayconvert

import (
	kitconvert "github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type TextConverterQuality = kitconvert.TextConverterQuality
type TextRoute = kitconvert.TextRoute

const (
	TextConverterQualityGood        = kitconvert.TextConverterQualityGood
	TextConverterQualityFair        = kitconvert.TextConverterQualityFair
	TextConverterQualityDiscouraged = kitconvert.TextConverterQualityDiscouraged
)

func ResolveRoute(from, to types.RelayFormat, stream bool) (TextRoute, bool) {
	return kitconvert.ResolveRoute(from, to, stream)
}
