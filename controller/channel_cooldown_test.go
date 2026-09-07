package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultiKeyCooldownManagement protects scope isolation, legacy counts and sensitive writes.
func TestMultiKeyCooldownManagement(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	server := miniredis.RunT(t)
	oldRedis, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	client := common.RDB
	common.RedisEnabled = true
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		common.RDB, common.RedisEnabled = oldRedis, oldEnabled
	})
	channel := &model.Channel{Name: "management-fixture", Key: "KEY_A\nKEY_B", Status: 1, ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeySize: 2}}
	require.NoError(t, db.Create(channel).Error)
	prefix := fmt.Sprintf("newapi:multi-key-disable:{%d}:key:%s:model:", channel.Id, service.MultiKeyFingerprint("KEY_A"))
	for _, name := range []string{"MODEL_A", "MODEL_B"} {
		raw := fmt.Sprintf(`{"scope":"model","model":%q,"disabled_until":1,"version":"v1"}`, name)
		require.NoError(t, common.RDB.Set(context.Background(), prefix+service.MultiKeyFingerprint(name), raw, time.Hour).Err())
	}
	for _, role := range []int{0, common.RoleRootUser} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("role", role)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key", strings.NewReader(fmt.Sprintf(`{"channel_id":%d,"action":"get_key_status"}`, channel.Id)))
		ManageMultiKeys(c)
		var response struct {
			Success bool                   `json:"success"`
			Data    MultiKeyStatusResponse `json:"data"`
		}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		require.True(t, response.Success, w.Body.String())
		assert.Equal(t, 2, response.Data.EnabledCount)
		assert.Zero(t, response.Data.TemporaryDisabledCount)
		require.Len(t, response.Data.Keys, 2)
		assert.Len(t, response.Data.Keys[0].Cooldowns, 2)
		assert.Equal(t, role == common.RoleRootUser, response.Data.CanManageCooldowns)
		w = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(w)
		c.Set("role", role)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/channel/multi_key", strings.NewReader(fmt.Sprintf(`{"channel_id":%d,"key_index":0,"action":"clear_model_cooldown","model":"MODEL_A"}`, channel.Id)))
		ManageMultiKeys(c)
		assert.Equal(t, role != common.RoleRootUser, server.Exists(prefix+service.MultiKeyFingerprint("MODEL_A")), w.Body.String())
		assert.True(t, server.Exists(prefix+service.MultiKeyFingerprint("MODEL_B")))
	}
}
