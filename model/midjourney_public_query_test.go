package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetAllUserMidjourneySelectsPublicColumns verifies ordinary drawing queries
// omit owner, channel and internal state fields.
func TestGetAllUserMidjourneySelectsPublicColumns(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&Midjourney{
		Id:         9,
		Code:       1,
		UserId:     23,
		ChannelId:  17,
		MjId:       "mj-public",
		State:      "internal-state",
		Prompt:     "user prompt",
		ImageUrl:   "https://supplier.example/image.png",
		Status:     "SUCCESS",
		Properties: `{"finalPrompt":"public prompt"}`,
	}).Error)

	items := GetAllUserTask(23, 0, 20, TaskQueryParams{})
	require.Len(t, items, 1)
	item := items[0]
	assert.Zero(t, item.Id)
	assert.Zero(t, item.Code)
	assert.Zero(t, item.UserId)
	assert.Zero(t, item.ChannelId)
	assert.Empty(t, item.State)
	assert.Equal(t, "mj-public", item.MjId)
	assert.Equal(t, "user prompt", item.Prompt)
}
