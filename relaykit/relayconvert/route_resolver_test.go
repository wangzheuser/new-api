package relayconvert

import (
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRouteTextProtocolMatrix(t *testing.T) {
	formats := []types.RelayFormat{
		types.RelayFormatOpenAI,
		types.RelayFormatOpenAIResponses,
		types.RelayFormatClaude,
		types.RelayFormatGemini,
	}
	for _, stream := range []bool{false, true} {
		for _, from := range formats {
			for _, to := range formats {
				if from == to {
					continue
				}
				name := string(from) + "_to_" + string(to)
				if stream {
					name += "_stream"
				}
				t.Run(name, func(t *testing.T) {
					route, ok := ResolveRoute(from, to, stream)
					require.True(t, ok)
					assert.NotEmpty(t, route.RequestConverter)
					assert.NotEmpty(t, route.ResponseConverter)
					assert.NotEmpty(t, route.RequestSteps)
					assert.NotEmpty(t, route.ResponseSteps)
					assert.Equal(t, stream, route.Stream)
					expected := TextConverterQualityFair
					if (from == types.RelayFormatOpenAI && to == types.RelayFormatOpenAIResponses) ||
						(from == types.RelayFormatOpenAIResponses && to == types.RelayFormatOpenAI) {
						expected = TextConverterQualityGood
					}
					if (from == types.RelayFormatClaude && to == types.RelayFormatGemini) ||
						(from == types.RelayFormatGemini && to == types.RelayFormatClaude) {
						expected = TextConverterQualityDiscouraged
					}
					assert.Equal(t, expected, route.Quality)
				})
			}
		}
	}
}

func TestResolveRouteRejectsSameOrUnknownFormat(t *testing.T) {
	_, ok := ResolveRoute(types.RelayFormatOpenAI, types.RelayFormatOpenAI, false)
	assert.False(t, ok)
	_, ok = ResolveRoute(types.RelayFormatOpenAI, types.RelayFormatEmbedding, false)
	assert.False(t, ok)
}
