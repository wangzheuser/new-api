package model_setting

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
)

// InputModalityConfigSource identifies which configuration scope supplied the effective declaration.
type InputModalityConfigSource string

const (
	InputModalitySourceChannel      InputModalityConfigSource = "channel"
	InputModalitySourceGlobal       InputModalityConfigSource = "global"
	InputModalitySourceUnconfigured InputModalityConfigSource = "unconfigured"
)

// ResolveModelInputModalities resolves an exact requested-model declaration using channel-first precedence.
func ResolveModelInputModalities(requestedModel string, channelSettings dto.ChannelSettings) ([]types.InputModality, bool, InputModalityConfigSource) {
	if modalities, exists := channelSettings.ModelInputModalities[requestedModel]; exists {
		return modalities, true, InputModalitySourceChannel
	}
	if modalities, exists := globalSettings.ModelInputModalities[requestedModel]; exists {
		return modalities, true, InputModalitySourceGlobal
	}
	return nil, false, InputModalitySourceUnconfigured
}

// ValidateModelInputModalitiesJSON validates one global option value before persistence.
func ValidateModelInputModalitiesJSON(value string) error {
	_, err := NormalizeModelInputModalitiesJSON(value)
	return err
}

// NormalizeModelInputModalitiesJSON validates and serializes one global declaration in stable order.
func NormalizeModelInputModalitiesJSON(value string) (string, error) {
	modalities := types.ModelInputModalities{}
	if err := common.UnmarshalJsonStr(value, &modalities); err != nil {
		return "", err
	}
	if modalities == nil {
		return "", fmt.Errorf("model input modalities must be a JSON object")
	}
	if err := modalities.Validate(); err != nil {
		return "", err
	}
	normalized, err := common.Marshal(modalities.Normalized())
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}
