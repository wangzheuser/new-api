package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestChannelValidateSettingsRejectsMySQLTextOverflow(t *testing.T) {
	setting := strings.Repeat("a", dto.MaxChannelSettingBytes+1)
	channel := &Channel{Setting: common.GetPointer(setting)}

	require.ErrorContains(t, channel.ValidateSettings(), "channel settings cannot exceed 64 KiB")
}
