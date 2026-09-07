package relay

import (
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"testing"
)

// TestFixedProtocolPassThrough rejects bypassing conversion for all client formats.
func TestFixedProtocolPassThrough(t *testing.T) {
	for _, api := range []int{constant.APITypeAnthropic, constant.APITypeGemini} {
		for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini} {
			info := &relaycommon.RelayInfo{RelayFormat: format, ChannelMeta: &relaycommon.ChannelMeta{ApiType: api}}
			native := api == constant.APITypeAnthropic && format == types.RelayFormatClaude || api == constant.APITypeGemini && format == types.RelayFormatGemini
			assert.Equal(t, native, textRouteAllowsPassThrough(info), "%d/%s", api, format)
		}
	}
}
