package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

	normalizedPolicy := &dto.ChannelProtocolPolicy{
		Native: map[constant.EndpointType]dto.ProtocolCapability{
			constant.EndpointTypeAnthropic: {NonStream: true, Mode: dto.ProtocolHandlingModeNormalized},
		},
		MaxQuality: dto.ProtocolConversionQualityFair,
	}
	normalizedPassThrough := &Channel{Type: constant.ChannelTypeOpenAI}
	normalizedPassThrough.SetSetting(dto.ChannelSettings{
		PassThroughBodyEnabled: true,
		ProtocolPolicy:         normalizedPolicy,
	})
	assert.ErrorContains(t, normalizedPassThrough.ValidateSettings(), "normalized protocol handling conflicts")
}

func TestChannelValidateSettingsInputModalities(t *testing.T) {
	validSetting := `{"force_format":true,"model_input_modalities":{"model-a":["image","text"]}}`
	valid := &Channel{Models: "model-a", Setting: &validSetting}
	require.NoError(t, valid.ValidateSettings())
	assert.JSONEq(t, `{"force_format":true,"model_input_modalities":{"model-a":["text","image"]}}`, *valid.Setting)

	invalid := &Channel{Models: "model-a"}
	invalid.SetSetting(dto.ChannelSettings{ModelInputModalities: types.ModelInputModalities{
		"model-a": {types.InputModalityImage},
	}})
	assert.ErrorContains(t, invalid.ValidateSettings(), "text input modality is required")
}

func TestChannelValidateSettingsPrunesRemovedInputModalities(t *testing.T) {
	setting := `{"force_format":true,"future_field":{"enabled":true},"model_input_modalities":{"model-a":["image","text"],"model-b":["text"]}}`
	channel := &Channel{Models: " model-a ,Model-A", Setting: &setting}

	require.NoError(t, channel.ValidateSettings())
	assert.JSONEq(t, `{"force_format":true,"future_field":{"enabled":true},"model_input_modalities":{"model-a":["text","image"]}}`, *channel.Setting)

	channel.Models = "Model-A"
	require.NoError(t, channel.ValidateSettings())
	assert.JSONEq(t, `{"force_format":true,"future_field":{"enabled":true}}`, *channel.Setting)
}

func TestEditChannelByTagPrunesRemovedInputModalities(t *testing.T) {
	previousDB := DB
	dsn := fmt.Sprintf("file:channel-settings-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	t.Cleanup(func() { DB = previousDB })

	tag := "shared-tag"
	setting := `{"future_field":true,"model_input_modalities":{"model-a":["text"],"model-b":["text"]}}`
	channel := Channel{
		Type:    constant.ChannelTypeOpenAI,
		Key:     "test-key",
		Status:  common.ChannelStatusEnabled,
		Name:    "tagged channel",
		Models:  "model-a,model-b",
		Group:   "default",
		Tag:     &tag,
		Setting: &setting,
	}
	require.NoError(t, db.Create(&channel).Error)

	models := "model-a"
	require.NoError(t, EditChannelByTag(tag, nil, nil, &models, nil, nil, nil, nil, nil))

	var stored Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	assert.Equal(t, models, stored.Models)
	require.NotNil(t, stored.Setting)
	assert.JSONEq(t, `{"future_field":true,"model_input_modalities":{"model-a":["text"]}}`, *stored.Setting)
}
