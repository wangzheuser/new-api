package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateOptionValidatesAndSynchronizesModelInputModalities(t *testing.T) {
	db := setupPerformanceOptionTest(t)
	settings := model_setting.GetGlobalSettings()
	previous := settings.ModelInputModalities
	settings.ModelInputModalities = types.ModelInputModalities{}
	t.Cleanup(func() { settings.ModelInputModalities = previous })

	valid := `{"source":["image","text"]}`
	normalized := `{"source":["text","image"]}`
	status, response := updatePerformanceOption(t, "global.model_input_modalities", valid)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, true, response["success"])
	assert.Equal(t, types.ModelInputModalities{
		"source": {types.InputModalityText, types.InputModalityImage},
	}, settings.ModelInputModalities)

	var stored model.Option
	require.NoError(t, db.First(&stored, "key = ?", "global.model_input_modalities").Error)
	assert.Equal(t, normalized, stored.Value)

	status, response = updatePerformanceOption(t, "global.model_input_modalities", `{"source":["image"]}`)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, response["success"])
	assert.NotEmpty(t, response["message"])
	require.NoError(t, db.First(&stored, "key = ?", "global.model_input_modalities").Error)
	assert.Equal(t, normalized, stored.Value)
	assert.Equal(t, types.ModelInputModalities{
		"source": {types.InputModalityText, types.InputModalityImage},
	}, settings.ModelInputModalities)

	status, response = updatePerformanceOption(t, "global.model_input_modalities", `{"source":`)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, false, response["success"])
	require.NoError(t, db.First(&stored, "key = ?", "global.model_input_modalities").Error)
	assert.Equal(t, normalized, stored.Value)
	assert.Equal(t, types.ModelInputModalities{
		"source": {types.InputModalityText, types.InputModalityImage},
	}, settings.ModelInputModalities)
}
