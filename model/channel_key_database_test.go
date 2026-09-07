package model

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestMultiKeyDatabaseCompatibility runs the key recovery contract on a disposable external database.
func TestMultiKeyDatabaseCompatibility(t *testing.T) {
	dsn := os.Getenv("MULTIKEY_TEST_DSN")
	if dsn == "" {
		t.Skip("set MULTIKEY_TEST_DSN to a disposable MySQL or PostgreSQL database")
	}
	driver := os.Getenv("MULTIKEY_TEST_DRIVER")
	var dialect gorm.Dialector
	switch driver {
	case "mysql":
		dialect = mysql.Open(dsn)
	case "postgres":
		dialect = postgres.Open(dsn)
	default:
		t.Fatal("unsupported test database")
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	oldDB, oldMemory, oldType := DB, common.MemoryCacheEnabled, common.MainDatabaseType()
	DB = db
	common.MemoryCacheEnabled = false
	if driver == "mysql" {
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	} else {
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	}
	t.Cleanup(func() {
		DB = oldDB
		common.MemoryCacheEnabled = oldMemory
		common.SetMainDatabaseType(oldType)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	channel := &Channel{Name: t.Name(), Key: "KEY_A\nKEY_B", Status: 1, ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyMode: constant.MultiKeyModeRandom}}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&Ability{Group: "default", Model: "MODEL", ChannelId: channel.Id, Enabled: true}).Error)
	require.True(t, UpdateChannelStatus(channel.Id, "KEY_A", common.ChannelStatusAutoDisabled, "invalid credential"))
	loaded, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, loaded.Status)
	key, _, apiErr := loaded.Snapshot().GetNextEnabledKey()
	require.Nil(t, apiErr)
	assert.Equal(t, "KEY_B", key)
	require.True(t, UpdateChannelStatus(channel.Id, "KEY_B", common.ChannelStatusAutoDisabled, "invalid credential"))
	loaded, err = GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, loaded.Status)
	require.True(t, UpdateChannelStatus(channel.Id, "KEY_A", common.ChannelStatusEnabled, ""))
	loaded, err = GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, loaded.Status)
	assert.NotContains(t, loaded.ChannelInfo.MultiKeyDisabledReason, 0)
	var ability Ability
	require.NoError(t, db.Where("channel_id = ?", channel.Id).First(&ability).Error)
	assert.True(t, ability.Enabled)
}
