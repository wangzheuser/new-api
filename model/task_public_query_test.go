package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTaskGetAllUserTaskSelectsOnlyPublicColumns verifies ordinary list queries
// omit routing/private fields and load response data only for Suno playback.
func TestTaskGetAllUserTaskSelectsOnlyPublicColumns(t *testing.T) {
	truncateTables(t)
	insertTask(t, &Task{
		TaskID:     "task-video",
		Platform:   "kling",
		UserId:     1,
		Group:      "internal-group",
		ChannelId:  17,
		Status:     TaskStatusSuccess,
		Properties: Properties{UpstreamModelName: "provider-model"},
		PrivateData: TaskPrivateData{
			Key:       "provider-key",
			ResultURL: "https://supplier.example/video.mp4",
		},
		Data: []byte(`{"provider_task_id":"upstream-video"}`),
	})
	insertTask(t, &Task{
		TaskID:    "task-suno",
		Platform:  constant.TaskPlatformSuno,
		UserId:    1,
		ChannelId: 18,
		Status:    TaskStatusSuccess,
		Data:      []byte(`[{"id":"clip-public","audio_url":"https://media.example/audio.mp3"}]`),
	})
	// 历史 SQLite 数据可能以 TEXT 存储 JSON，公开查询需同时兼容 TEXT/BLOB。
	require.NoError(t, DB.Exec(
		"UPDATE tasks SET data = ? WHERE task_id = ?",
		`[{"id":"clip-public","audio_url":"https://media.example/audio.mp3"}]`,
		"task-suno",
	).Error)

	tasks := TaskGetAllUserTask(1, 0, 20, SyncTaskQueryParams{})
	require.Len(t, tasks, 2)
	byID := map[string]*Task{}
	for _, task := range tasks {
		byID[task.TaskID] = task
	}

	video := byID["task-video"]
	require.NotNil(t, video)
	assert.Zero(t, video.ChannelId)
	assert.Empty(t, video.Group)
	assert.Equal(t, Properties{}, video.Properties)
	assert.Equal(t, TaskPrivateData{}, video.PrivateData)
	assert.Empty(t, video.Data)

	suno := byID["task-suno"]
	require.NotNil(t, suno)
	assert.JSONEq(t, `[{"id":"clip-public","audio_url":"https://media.example/audio.mp3"}]`, string(suno.Data))
}
