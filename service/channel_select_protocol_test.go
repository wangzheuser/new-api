package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupChannelSelectProtocolTestDB creates isolated channel and ability fixtures.
func setupChannelSelectProtocolTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousMemoryCache := common.MemoryCacheEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	dsn := fmt.Sprintf("file:channel-select-protocol-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	t.Cleanup(func() {
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.MemoryCacheEnabled = previousMemoryCache
		common.SetMainDatabaseType(previousMainDatabaseType)
		common.SetLogDatabaseType(previousLogDatabaseType)
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// createProtocolSelectionChannel inserts one deterministic routing candidate.
func createProtocolSelectionChannel(t *testing.T, db *gorm.DB, name string, priority int64, endpointType constant.EndpointType) *model.Channel {
	t.Helper()
	channel := &model.Channel{
		Type:        constant.ChannelTypeOpenAI,
		Key:         "TOKEN",
		Status:      common.ChannelStatusEnabled,
		Name:        name,
		Models:      "MODEL_X",
		Group:       "vip",
		Priority:    &priority,
		Weight:      common.GetPointer(uint(100)),
		CreatedTime: common.GetTimestamp(),
	}
	channel.SetSetting(dto.ChannelSettings{ProtocolPolicy: &dto.ChannelProtocolPolicy{
		Native: map[constant.EndpointType]dto.ProtocolCapability{
			endpointType: {NonStream: true, Stream: true},
		},
		AutoConvert: false,
		MaxQuality:  dto.ProtocolConversionQualityFair,
	}})
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "vip",
		Model:     "MODEL_X",
		ChannelId: channel.Id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    100,
	}).Error)
	return channel
}

func TestCacheGetRandomSatisfiedChannelWithRouteSkipsHigherPriorityMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupChannelSelectProtocolTestDB(t)
	highPriority := createProtocolSelectionChannel(t, db, "chat-only", 10, constant.EndpointTypeOpenAI)
	compatible := createProtocolSelectionChannel(t, db, "responses", 1, constant.EndpointTypeOpenAIResponse)

	for _, memoryCacheEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			common.MemoryCacheEnabled = memoryCacheEnabled
			if memoryCacheEnabled {
				model.InitChannelCache()
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			param := &RetryParam{
				Ctx:         ctx,
				TokenGroup:  "vip",
				ModelName:   "MODEL_X",
				RequestPath: "/v1/responses",
				Retry:       common.GetPointer(0),
			}

			selected, plan, group, err := CacheGetRandomSatisfiedChannelWithRoute(param)

			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, "vip", group)
			assert.Equal(t, compatible.Id, selected.Id)
			require.NotNil(t, plan)
			assert.Equal(t, types.ChannelRouteModeNative, plan.RouteMode)
			_, excluded := param.ExcludedChannelIDs[highPriority.Id]
			assert.True(t, excluded)
		})
	}
}
