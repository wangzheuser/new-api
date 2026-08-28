package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecalculateMultiKeyStatus(t *testing.T) {
	tests := []struct {
		name       string
		statusList map[int]int
		wantStatus int
	}{
		{
			name:       "missing status means enabled",
			statusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusManuallyDisabled},
			wantStatus: common.ChannelStatusEnabled,
		},
		{
			name:       "any enabled key keeps channel enabled",
			statusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusEnabled, 2: common.ChannelStatusManuallyDisabled},
			wantStatus: common.ChannelStatusEnabled,
		},
		{
			name:       "auto-disabled key determines unavailable channel status",
			statusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusManuallyDisabled, 2: common.ChannelStatusManuallyDisabled},
			wantStatus: common.ChannelStatusAutoDisabled,
		},
		{
			name:       "all manually disabled keys make channel manually disabled",
			statusList: map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusManuallyDisabled, 2: common.ChannelStatusManuallyDisabled},
			wantStatus: common.ChannelStatusManuallyDisabled,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel := &Channel{
				Key:    "key-0\nkey-1\nkey-2",
				Status: common.ChannelStatusAutoDisabled,
				ChannelInfo: ChannelInfo{
					IsMultiKey:         true,
					MultiKeyStatusList: test.statusList,
				},
			}

			channel.RecalculateMultiKeyStatus()

			assert.Equal(t, test.wantStatus, channel.Status)
		})
	}
}

func TestHandlerMultiKeyUpdateClearsRecoveredKeyMetadata(t *testing.T) {
	channel := &Channel{
		Key:       "key-0\nkey-1",
		Status:    common.ChannelStatusAutoDisabled,
		OtherInfo: `{"status_reason":"All keys are disabled","status_time":123}`,
		ChannelInfo: ChannelInfo{
			IsMultiKey:             true,
			MultiKeyStatusList:     map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusAutoDisabled},
			MultiKeyDisabledReason: map[int]string{0: "reason-0", 1: "reason-1"},
			MultiKeyDisabledTime:   map[int]int64{0: 100, 1: 200},
		},
	}

	changed := handlerMultiKeyUpdate(channel, "key-0", common.ChannelStatusEnabled, "")

	require.True(t, changed)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	assert.NotContains(t, channel.ChannelInfo.MultiKeyStatusList, 0)
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledReason, 0)
	assert.NotContains(t, channel.ChannelInfo.MultiKeyDisabledTime, 0)
	assert.Equal(t, common.ChannelStatusAutoDisabled, channel.ChannelInfo.MultiKeyStatusList[1])
	assert.NotContains(t, channel.GetOtherInfo(), "status_reason")
	assert.NotContains(t, channel.GetOtherInfo(), "status_time")
}

func TestUpdateChannelStatusRecoversPartialMultiKeyChannel(t *testing.T) {
	resetChannelStatusTestTables(t)
	channel := createChannelStatusTestFixture(t, 7101, common.ChannelStatusEnabled, map[int]int{
		0: common.ChannelStatusAutoDisabled,
	})
	InitChannelCache()

	changed := UpdateChannelStatus(channel.Id, "key-0", common.ChannelStatusEnabled, "")

	require.True(t, changed)
	var saved Channel
	require.NoError(t, DB.First(&saved, "id = ?", channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, saved.Status)
	assert.NotContains(t, saved.ChannelInfo.MultiKeyStatusList, 0)
	cached, err := CacheGetChannel(channel.Id)
	require.NoError(t, err)
	assert.NotContains(t, cached.ChannelInfo.MultiKeyStatusList, 0)
}

func TestUpdateChannelStatusReaddsRecoveredChannelToRouteCache(t *testing.T) {
	resetChannelStatusTestTables(t)
	channel := createChannelStatusTestFixture(t, 7102, common.ChannelStatusAutoDisabled, map[int]int{
		0: common.ChannelStatusAutoDisabled,
		1: common.ChannelStatusAutoDisabled,
	})
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channel.Id).Update("enabled", false).Error)
	InitChannelCache()
	require.False(t, IsChannelEnabledForGroupModel("default", "test-model", channel.Id))

	changed := UpdateChannelStatus(channel.Id, "key-0", common.ChannelStatusEnabled, "")

	require.True(t, changed)
	assert.True(t, IsChannelEnabledForGroupModel("default", "test-model", channel.Id))
	var ability Ability
	require.NoError(t, DB.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
}

// resetChannelStatusTestTables isolates channel status and route-cache test state.
func resetChannelStatusTestTables(t *testing.T) {
	t.Helper()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	InitChannelCache()
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
		require.NoError(t, DB.Exec("DELETE FROM channels").Error)
		InitChannelCache()
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})
}

// createChannelStatusTestFixture creates one multi-key channel and its routable ability.
func createChannelStatusTestFixture(t *testing.T, channelID int, status int, statusList map[int]int) *Channel {
	t.Helper()
	channel := &Channel{
		Id:     channelID,
		Type:   1,
		Key:    "key-0\nkey-1",
		Status: status,
		Name:   "multi-key-test",
		Models: "test-model",
		Group:  "default",
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyStatusList: statusList,
		},
	}
	require.NoError(t, DB.Create(channel).Error)
	require.NoError(t, DB.Create(&Ability{
		Group:     "default",
		Model:     "test-model",
		ChannelId: channel.Id,
		Enabled:   status == common.ChannelStatusEnabled,
	}).Error)
	return channel
}
