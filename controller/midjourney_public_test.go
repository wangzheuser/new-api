package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMidjourneyTasksToUserDto verifies ordinary drawing responses omit routing details.
func TestMidjourneyTasksToUserDto(t *testing.T) {
	items := midjourneyTasksToUserDto([]*model.Midjourney{{
		Id:         7,
		UserId:     11,
		ChannelId:  17,
		MjId:       "mj-public",
		Action:     "IMAGINE",
		Prompt:     "user prompt",
		ImageUrl:   "https://supplier.example/image.png",
		Status:     "FAILURE",
		FailReason: "channel 17 upstream response",
		Buttons:    `[{"customId":"U1","label":"U1"}]`,
		Properties: `{"finalPrompt":"final","finalZhPrompt":"最终"}`,
		VideoUrls:  `[{"url":"https://media.example/video.mp4"}]`,
	}})

	require.Len(t, items, 1)
	item := items[0]
	assert.Equal(t, "mj-public", item.MjId)
	assert.Equal(t, "/mj/image/mj-public", item.ImageUrl)
	assert.Equal(t, "任务处理失败", item.FailReason)
	require.NotNil(t, item.Properties)
	assert.Equal(t, "final", item.Properties.FinalPrompt)

	encoded, err := common.Marshal(item)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "channel_id")
	assert.NotContains(t, string(encoded), "user_id")
	assert.NotContains(t, string(encoded), "supplier.example/image")
	assert.NotContains(t, string(encoded), "channel 17")
}
