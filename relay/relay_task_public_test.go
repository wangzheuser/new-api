package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTaskModel2UserDto verifies ordinary task responses expose only public media fields.
func TestTaskModel2UserDto(t *testing.T) {
	sunoData, err := common.Marshal([]dto.SunoSong{{
		ID:                "clip-public",
		AudioURL:          "https://media.example/audio.mp3",
		MajorModelVersion: "provider-version",
		ModelName:         "provider-model",
		Title:             "song",
		Metadata: dto.SunoMetadata{
			Tags:          "pop",
			AudioPromptID: "internal-prompt-id",
			Duration:      30.5,
			ErrorMessage:  "internal-error",
		},
	}})
	require.NoError(t, err)

	public := TaskModel2UserDto(&model.Task{
		TaskID:     "task-public",
		Platform:   constant.TaskPlatformSuno,
		Status:     model.TaskStatusSuccess,
		FailReason: "provider response body",
		Data:       sunoData,
	})

	assert.Equal(t, "suno", public.Platform)
	assert.Empty(t, public.FailReason)
	assert.Empty(t, public.ResultURL)
	songs, ok := public.Data.([]dto.PublicSunoSong)
	require.True(t, ok)
	require.Len(t, songs, 1)
	assert.Equal(t, "clip-public", songs[0].ID)
	assert.Equal(t, "pop", songs[0].Metadata.Tags)

	encoded, err := common.Marshal(public)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "provider-version")
	assert.NotContains(t, string(encoded), "provider-model")
	assert.NotContains(t, string(encoded), "internal-prompt-id")
	assert.NotContains(t, string(encoded), "internal-error")
}

// TestTaskModel2UserDtoUsesLocalVideoProxy verifies video responses never expose upstream URLs.
func TestTaskModel2UserDtoUsesLocalVideoProxy(t *testing.T) {
	public := TaskModel2UserDto(&model.Task{
		TaskID:     "task-video",
		Platform:   "kling",
		Status:     model.TaskStatusSuccess,
		FailReason: "https://supplier.example/result.mp4",
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://supplier.example/private.mp4",
		},
		Data: []byte(`{"provider_task_id":"upstream-id"}`),
	})

	assert.Equal(t, "video", public.Platform)
	assert.Equal(t, "/v1/videos/task-video/content", public.ResultURL)
	assert.Empty(t, public.FailReason)
	assert.Nil(t, public.Data)
}
