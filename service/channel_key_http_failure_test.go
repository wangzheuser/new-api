package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
)

// TestMultiKeyHTTPFailureOverride distinguishes original HTTP failures from local and mapped errors.
func TestMultiKeyHTTPFailureOverride(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })
	channel := &model.Channel{
		AutoBan: common.GetPointer(1), ChannelInfo: model.ChannelInfo{IsMultiKey: true},
		OtherSettings: `{"multi_key_auto_disable_override":{"temporary_status_codes":"429,500-503","persistent_status_codes":"401","temporary_disable_minutes":1}}`,
	}
	for _, tt := range []struct {
		name     string
		upstream int
		want     MultiKeyFailureAction
	}{
		{"upstream failure", 500, MultiKeyFailureTemporary},
		{"gateway failure", 502, MultiKeyFailureTemporary},
		{"local conversion failure", 0, MultiKeyFailureNone},
		{"mapped client error", 400, MultiKeyFailureNone},
		{"expired credential", 401, MultiKeyFailurePersistent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := types.NewErrorWithStatusCode(errors.New("failure"), types.ErrorCodeBadResponseStatusCode, 500)
			if tt.upstream != 0 {
				types.ErrOptionWithUpstreamStatusCode(tt.upstream)(err)
			}
			action, _ := ClassifyMultiKeyFailure(channel, err)
			assert.Equal(t, tt.want, action)
		})
	}
	channel.OtherSettings = ""
	action, _ := ClassifyMultiKeyFailure(channel, upstreamStatusError(500, "failure"))
	assert.Equal(t, MultiKeyFailureNone, action, "other channels retain the default policy")
}
