package types

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelInputModalitiesValidate(t *testing.T) {
	tests := []struct {
		name    string
		value   ModelInputModalities
		wantErr bool
	}{
		{name: "empty configuration", value: ModelInputModalities{}},
		{name: "text only", value: ModelInputModalities{"model-a": {InputModalityText}}},
		{name: "text and image", value: ModelInputModalities{"model-a": {InputModalityText, InputModalityImage}}},
		{name: "empty model", value: ModelInputModalities{"": {InputModalityText}}, wantErr: true},
		{name: "model whitespace", value: ModelInputModalities{" model-a": {InputModalityText}}, wantErr: true},
		{name: "model too long", value: ModelInputModalities{string(make([]byte, 256)): {InputModalityText}}, wantErr: true},
		{name: "empty modalities", value: ModelInputModalities{"model-a": {}}, wantErr: true},
		{name: "missing text", value: ModelInputModalities{"model-a": {InputModalityImage}}, wantErr: true},
		{name: "unknown modality", value: ModelInputModalities{"model-a": {InputModalityText, "audio"}}, wantErr: true},
		{name: "duplicate modality", value: ModelInputModalities{"model-a": {InputModalityText, InputModalityText}}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.value.Validate()
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestModelInputModalitiesValidateEntryLimit(t *testing.T) {
	value := make(ModelInputModalities, MaxModelInputModalityEntries+1)
	for index := 0; index <= MaxModelInputModalityEntries; index++ {
		value[fmt.Sprintf("model-%d", index)] = []InputModality{InputModalityText}
	}
	require.Error(t, value.Validate())
}

func TestModelInputModalitiesNormalized(t *testing.T) {
	value := ModelInputModalities{
		"vision": {InputModalityImage, InputModalityText},
		"text":   {InputModalityText},
	}

	assert.Equal(t, ModelInputModalities{
		"vision": {InputModalityText, InputModalityImage},
		"text":   {InputModalityText},
	}, value.Normalized())
}

func TestModelInputModalitiesFilterForModels(t *testing.T) {
	value := ModelInputModalities{
		"model-a": {InputModalityText},
		"Model-A": {InputModalityText, InputModalityImage},
		"model-b": {InputModalityText},
	}

	assert.Equal(t, ModelInputModalities{
		"model-a": {InputModalityText},
		"Model-A": {InputModalityText, InputModalityImage},
	}, value.FilterForModels([]string{" model-a ", "Model-A", "model-a"}))
}
