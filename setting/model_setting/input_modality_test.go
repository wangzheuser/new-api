package model_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveModelInputModalities(t *testing.T) {
	previous := globalSettings.ModelInputModalities
	globalSettings.ModelInputModalities = types.ModelInputModalities{
		"global-only":    {types.InputModalityText},
		"channel-allows": {types.InputModalityText},
		"shared":         {types.InputModalityText, types.InputModalityImage},
		"upstream":       {types.InputModalityText, types.InputModalityImage},
	}
	t.Cleanup(func() { globalSettings.ModelInputModalities = previous })

	tests := []struct {
		name       string
		model      string
		settings   dto.ChannelSettings
		want       []types.InputModality
		configured bool
		source     InputModalityConfigSource
	}{
		{
			name:       "channel overrides global",
			model:      "shared",
			settings:   dto.ChannelSettings{ModelInputModalities: types.ModelInputModalities{"shared": {types.InputModalityText}}},
			want:       []types.InputModality{types.InputModalityText},
			configured: true,
			source:     InputModalitySourceChannel,
		},
		{
			name:       "global fallback",
			model:      "global-only",
			want:       []types.InputModality{types.InputModalityText},
			configured: true,
			source:     InputModalitySourceGlobal,
		},
		{
			name:  "channel image declaration overrides global text only",
			model: "channel-allows",
			settings: dto.ChannelSettings{ModelInputModalities: types.ModelInputModalities{
				"channel-allows": {types.InputModalityText, types.InputModalityImage},
			}},
			want:       []types.InputModality{types.InputModalityText, types.InputModalityImage},
			configured: true,
			source:     InputModalitySourceChannel,
		},
		{
			name:   "unconfigured requested model ignores upstream declaration",
			model:  "source",
			source: InputModalitySourceUnconfigured,
		},
		{
			name:   "model names match exactly",
			model:  "GLOBAL-ONLY",
			source: InputModalitySourceUnconfigured,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, configured, source := ResolveModelInputModalities(test.model, test.settings)
			assert.Equal(t, test.want, got)
			assert.Equal(t, test.configured, configured)
			assert.Equal(t, test.source, source)
		})
	}
}

func TestValidateModelInputModalitiesJSON(t *testing.T) {
	require.NoError(t, ValidateModelInputModalitiesJSON(`{"model-a":["text","image"]}`))
	require.Error(t, ValidateModelInputModalitiesJSON(`{"model-a":["image"]}`))
	require.Error(t, ValidateModelInputModalitiesJSON(`{"model-a":`))
	require.Error(t, ValidateModelInputModalitiesJSON(`null`))

	normalized, err := NormalizeModelInputModalitiesJSON(`{"model-a":["image","text"]}`)
	require.NoError(t, err)
	assert.Equal(t, `{"model-a":["text","image"]}`, normalized)
}
