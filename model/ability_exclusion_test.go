package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetChannelHardExclusion(t *testing.T) {
	previousDB := DB
	previousMainDBType := common.MainDatabaseType()
	dsn := fmt.Sprintf("file:channel-exclusion-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	initCol()
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousMainDBType)
		initCol()
	})

	source := &Channel{Name: "source", Status: common.ChannelStatusEnabled}
	target := &Channel{Name: "target", Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(source).Error)
	require.NoError(t, DB.Create(target).Error)
	highPriority := int64(10)
	lowPriority := int64(0)
	require.NoError(t, DB.Create([]Ability{
		{Group: "default", Model: "MODEL_B", ChannelId: source.Id, Enabled: true, Priority: &highPriority},
		{Group: "default", Model: "MODEL_B", ChannelId: target.Id, Enabled: true, Priority: &lowPriority},
	}).Error)

	selected, err := GetChannel("default", "MODEL_B", 0, "/v1/chat/completions", nil, map[int]struct{}{source.Id: {}})

	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, target.Id, selected.Id)
}

func TestGetRandomSatisfiedChannelMemoryCacheHardExclusion(t *testing.T) {
	previousMemoryCache := common.MemoryCacheEnabled
	channelSyncLock.Lock()
	previousGroups := group2model2channels
	previousChannels := channelsIDM
	previousAdvancedConfigs := channel2advancedCustomConfig
	highPriority := int64(10)
	lowPriority := int64(0)
	group2model2channels = map[string]map[string][]int{
		"default": {"MODEL_B": {1, 2}},
	}
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Name: "source", Status: common.ChannelStatusEnabled, Priority: &highPriority},
		2: {Id: 2, Name: "target", Status: common.ChannelStatusEnabled, Priority: &lowPriority},
	}
	channel2advancedCustomConfig = map[int]*dto.AdvancedCustomConfig{}
	channelSyncLock.Unlock()
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousMemoryCache
		channelSyncLock.Lock()
		group2model2channels = previousGroups
		channelsIDM = previousChannels
		channel2advancedCustomConfig = previousAdvancedConfigs
		channelSyncLock.Unlock()
	})

	selected, err := GetRandomSatisfiedChannel("default", "MODEL_B", 0, "/v1/chat/completions", nil, map[int]struct{}{1: {}})

	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 2, selected.Id)
}
