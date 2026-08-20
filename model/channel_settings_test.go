package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelValidateSettingsRejectsMySQLTextOverflow(t *testing.T) {
	setting := strings.Repeat("a", dto.MaxChannelSettingBytes+1)
	channel := &Channel{Setting: common.GetPointer(setting)}

	require.ErrorContains(t, channel.ValidateSettings(), "channel settings cannot exceed 64 KiB")
}

func TestChannelValidateSettingsProtocolPolicyScopeAndPassThrough(t *testing.T) {
	policy := &dto.ChannelProtocolPolicy{
		Native: map[constant.EndpointType]dto.ProtocolCapability{
			constant.EndpointTypeOpenAI: {NonStream: true},
		},
		AutoConvert: true,
		MaxQuality:  dto.ProtocolConversionQualityFair,
	}

	valid := &Channel{Type: constant.ChannelTypeOpenAI}
	valid.SetSetting(dto.ChannelSettings{ProtocolPolicy: policy})
	require.NoError(t, valid.ValidateSettings())

	passThrough := &Channel{Type: constant.ChannelTypeOpenAI}
	passThrough.SetSetting(dto.ChannelSettings{
		PassThroughBodyEnabled: true,
		ProtocolPolicy:         policy,
	})
	assert.ErrorContains(t, passThrough.ValidateSettings(), "conflicts with request body pass-through")

	unsupportedType := &Channel{Type: constant.ChannelTypeAnthropic}
	unsupportedType.SetSetting(dto.ChannelSettings{ProtocolPolicy: policy})
	assert.ErrorContains(t, unsupportedType.ValidateSettings(), "standard compatible channels")
}

func TestChannelValidateSettingsInputModalities(t *testing.T) {
	validSetting := `{"force_format":true,"model_input_modalities":{"model-a":["image","text"]}}`
	valid := &Channel{Setting: &validSetting}
	require.NoError(t, valid.ValidateSettings())
	assert.JSONEq(t, `{"force_format":true,"model_input_modalities":{"model-a":["text","image"]}}`, *valid.Setting)

	invalid := &Channel{}
	invalid.SetSetting(dto.ChannelSettings{ModelInputModalities: types.ModelInputModalities{
		"model-a": {types.InputModalityImage},
	}})
	assert.ErrorContains(t, invalid.ValidateSettings(), "text input modality is required")
}
