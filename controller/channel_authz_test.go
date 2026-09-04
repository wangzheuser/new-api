package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelHasSensitiveChanges(t *testing.T) {
	baseURL := "https://api.example.com"
	headerOverride := `{"Authorization":"Bearer {api_key}"}`
	origin := &model.Channel{
		Type:           1,
		Key:            "old-key",
		BaseURL:        &baseURL,
		HeaderOverride: &headerOverride,
		Models:         "gpt-4o",
		Group:          "default",
	}

	t.Run("non-sensitive routing fields", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Models = "gpt-4o,gpt-4o-mini"
		updated.Group = "vip"

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
			"models": updated.Models,
			"group":  updated.Group,
		}))
	})

	t.Run("key change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Key = "new-key"

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"key": updated.Key}))
	})

	t.Run("base url change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		newBaseURL := "https://leak.example.com"
		updated.BaseURL = &newBaseURL

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"base_url": newBaseURL}))
	})

	t.Run("header override change", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		newHeaderOverride := `{"X-Key":"{api_key}"}`
		updated.HeaderOverride = &newHeaderOverride

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"header_override": newHeaderOverride}))
	})

	t.Run("omitted sensitive fields do not use zero values", func(t *testing.T) {
		updated := PatchChannel{}
		updated.Id = origin.Id
		updated.Priority = origin.Priority

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"priority": 10}))
	})

	t.Run("unknown field fails closed", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}

		assert.True(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"future_secret_field": "x"}))
	})

	t.Run("status is operational", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Status = common.ChannelStatusManuallyDisabled

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{"status": updated.Status}))
	})

	t.Run("read-only fields are ignored by sensitivity check", func(t *testing.T) {
		updated := PatchChannel{Channel: *origin}
		updated.Balance = 99
		updated.UsedQuota = 100
		updated.ResponseTime = 200

		assert.False(t, channelHasSensitiveChanges(&updated, origin, map[string]any{
			"balance":       updated.Balance,
			"used_quota":    updated.UsedQuota,
			"response_time": updated.ResponseTime,
		}))
	})
}

func TestClearChannelReadOnlyFields(t *testing.T) {
	channel := PatchChannel{Channel: model.Channel{
		CreatedTime:        11,
		TestTime:           22,
		ResponseTime:       33,
		Balance:            44.5,
		BalanceUpdatedTime: 55,
		UsedQuota:          66,
		Models:             "gpt-4o",
		Group:              "default",
	}}

	clearChannelReadOnlyFields(&channel, map[string]any{
		"created_time":         channel.CreatedTime,
		"test_time":            channel.TestTime,
		"response_time":        channel.ResponseTime,
		"balance":              channel.Balance,
		"balance_updated_time": channel.BalanceUpdatedTime,
		"used_quota":           channel.UsedQuota,
		"models":               channel.Models,
		"group":                channel.Group,
	})

	assert.Zero(t, channel.CreatedTime)
	assert.Zero(t, channel.TestTime)
	assert.Zero(t, channel.ResponseTime)
	assert.Zero(t, channel.Balance)
	assert.Zero(t, channel.BalanceUpdatedTime)
	assert.Zero(t, channel.UsedQuota)
	assert.Equal(t, "gpt-4o", channel.Models)
	assert.Equal(t, "default", channel.Group)
}

func TestUpdateChannelRejectsStatusField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/channel/",
		bytes.NewBufferString(`{"id":1,"status":2}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	UpdateChannel(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
}

func TestNormalizeChannelUpdatePayloadPrunesPersistedInputModalities(t *testing.T) {
	setting := `{"future_field":true,"model_input_modalities":{"model-a":["text","image"],"model-b":["text"]}}`
	origin := &model.Channel{
		Id:      1,
		Type:    1,
		Key:     "saved-key",
		Models:  "model-a,model-b",
		Setting: &setting,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	patch := PatchChannel{Channel: model.Channel{Id: origin.Id, Models: "model-a"}}
	rawBody := []byte(`{"id":1,"models":"model-a"}`)

	require.NoError(t, normalizeChannelUpdatePayload(
		&patch,
		origin,
		rawBody,
		map[string]any{"id": float64(1), "models": "model-a"},
	))
	require.NotNil(t, patch.Setting)
	assert.JSONEq(t, `{"future_field":true,"model_input_modalities":{"model-a":["text","image"]}}`, *patch.Setting)
	assert.True(t, origin.ChannelInfo.IsMultiKey)
}

func TestPrepareMultiKeyUpdate(t *testing.T) {
	random := string(constant.MultiKeyModeRandom)
	appendMode := "append"

	t.Run("promotes a single-key channel", func(t *testing.T) {
		origin := &model.Channel{Type: 1, Key: "saved-key"}
		patch := PatchChannel{
			Channel:      model.Channel{Type: 1, Key: "new-key"},
			MultiKeyMode: &random,
			KeyMode:      &appendMode,
		}

		require.NoError(t, prepareMultiKeyUpdate(&patch, origin, map[string]any{
			"key":            patch.Key,
			"key_mode":       appendMode,
			"multi_key_mode": random,
		}))
		assert.True(t, patch.ChannelInfo.IsMultiKey)
		assert.Equal(t, constant.MultiKeyModeRandom, patch.ChannelInfo.MultiKeyMode)
	})

	t.Run("leaves a regular single-key update unchanged", func(t *testing.T) {
		origin := &model.Channel{Type: 1, Key: "saved-key"}
		patch := PatchChannel{Channel: model.Channel{Type: 1, Key: "new-key"}}

		require.NoError(t, prepareMultiKeyUpdate(&patch, origin, map[string]any{
			"key": patch.Key,
		}))
		assert.False(t, patch.ChannelInfo.IsMultiKey)
	})

	t.Run("rejects incomplete or unsupported conversions", func(t *testing.T) {
		invalidMode := "invalid"
		codexOrigin := &model.Channel{Type: constant.ChannelTypeCodex, Key: "saved-key"}

		tests := []struct {
			name    string
			origin  *model.Channel
			patch   PatchChannel
			request map[string]any
		}{
			{
				name:   "empty key",
				origin: &model.Channel{Type: 1, Key: "saved-key"},
				patch: PatchChannel{
					Channel:      model.Channel{Type: 1},
					MultiKeyMode: &random,
					KeyMode:      &appendMode,
				},
				request: map[string]any{"key": "", "key_mode": appendMode, "multi_key_mode": random},
			},
			{
				name:   "invalid key mode",
				origin: &model.Channel{Type: 1, Key: "saved-key"},
				patch: PatchChannel{
					Channel:      model.Channel{Type: 1, Key: "new-key"},
					MultiKeyMode: &random,
					KeyMode:      &invalidMode,
				},
				request: map[string]any{"key": "new-key", "key_mode": invalidMode, "multi_key_mode": random},
			},
			{
				name:   "codex channel",
				origin: codexOrigin,
				patch: PatchChannel{
					Channel:      model.Channel{Type: constant.ChannelTypeCodex, Key: "new-key"},
					MultiKeyMode: &random,
					KeyMode:      &appendMode,
				},
				request: map[string]any{"key": "new-key", "key_mode": appendMode, "multi_key_mode": random},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				require.Error(t, prepareMultiKeyUpdate(&test.patch, test.origin, test.request))
				assert.False(t, test.patch.ChannelInfo.IsMultiKey)
			})
		}
	})
}

// updateChannelForTest executes one root-authorized channel update and returns its business response.
func updateChannelForTest(t *testing.T, body string) struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
} {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Set("role", common.RoleRootUser)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel/", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	UpdateChannel(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestUpdateChannelConvertsSingleKeyChannel(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	createChannel := func(key string) model.Channel {
		channel := model.Channel{
			Type:   1,
			Key:    key,
			Name:   "single-key channel",
			Models: "gpt-4o-mini",
			Group:  "default",
			Status: common.ChannelStatusEnabled,
		}
		require.NoError(t, db.Create(&channel).Error)
		return channel
	}

	t.Run("append keeps the saved key", func(t *testing.T) {
		channel := createChannel("saved-key")
		response := updateChannelForTest(t, fmt.Sprintf(
			`{"id":%d,"key":"new-key-a\nnew-key-b","key_mode":"append","multi_key_mode":"random"}`,
			channel.Id,
		))
		require.True(t, response.Success, response.Message)

		var updated model.Channel
		require.NoError(t, db.First(&updated, channel.Id).Error)
		assert.Equal(t, "saved-key\nnew-key-a\nnew-key-b", updated.Key)
		assert.True(t, updated.ChannelInfo.IsMultiKey)
		assert.Equal(t, 3, updated.ChannelInfo.MultiKeySize)
		assert.Equal(t, constant.MultiKeyModeRandom, updated.ChannelInfo.MultiKeyMode)
	})

	t.Run("replace stores only submitted keys", func(t *testing.T) {
		channel := createChannel("saved-key")
		response := updateChannelForTest(t, fmt.Sprintf(
			`{"id":%d,"key":"new-key-a\nnew-key-b","key_mode":"replace","multi_key_mode":"polling"}`,
			channel.Id,
		))
		require.True(t, response.Success, response.Message)

		var updated model.Channel
		require.NoError(t, db.First(&updated, channel.Id).Error)
		assert.Equal(t, "new-key-a\nnew-key-b", updated.Key)
		assert.True(t, updated.ChannelInfo.IsMultiKey)
		assert.Equal(t, 2, updated.ChannelInfo.MultiKeySize)
		assert.Equal(t, constant.MultiKeyModePolling, updated.ChannelInfo.MultiKeyMode)
	})

	t.Run("ordinary key replacement remains single-key", func(t *testing.T) {
		channel := createChannel("saved-key")
		response := updateChannelForTest(t, fmt.Sprintf(
			`{"id":%d,"key":"replacement-key"}`,
			channel.Id,
		))
		require.True(t, response.Success, response.Message)

		var updated model.Channel
		require.NoError(t, db.First(&updated, channel.Id).Error)
		assert.Equal(t, "replacement-key", updated.Key)
		assert.False(t, updated.ChannelInfo.IsMultiKey)
	})

	t.Run("invalid conversion does not change the saved channel", func(t *testing.T) {
		channel := createChannel("saved-key")
		response := updateChannelForTest(t, fmt.Sprintf(
			`{"id":%d,"key":"new-key","key_mode":"invalid","multi_key_mode":"random"}`,
			channel.Id,
		))
		assert.False(t, response.Success)

		var updated model.Channel
		require.NoError(t, db.First(&updated, channel.Id).Error)
		assert.Equal(t, "saved-key", updated.Key)
		assert.False(t, updated.ChannelInfo.IsMultiKey)
	})
}

func TestChannelStatusValidation(t *testing.T) {
	assert.True(t, isManageableChannelStatus(common.ChannelStatusEnabled))
	assert.True(t, isManageableChannelStatus(common.ChannelStatusManuallyDisabled))
	assert.False(t, isManageableChannelStatus(common.ChannelStatusAutoDisabled))
	assert.False(t, isManageableChannelStatus(0))
}

// TestChannelFieldsAreClassified guards the fail-closed sensitivity check: every
// JSON field of PatchChannel (including the embedded model.Channel) must be listed
// in channelSensitiveFields, channelNonSensitiveFields, or
// channelOperationalFields. A newly added field that is left unclassified will
// fail this test, forcing a conscious permission decision instead of silently
// defaulting either way.
func TestChannelFieldsAreClassified(t *testing.T) {
	classified := func(name string) bool {
		if _, ok := channelSensitiveFields[name]; ok {
			return true
		}
		if _, ok := channelNonSensitiveFields[name]; ok {
			return true
		}
		if _, ok := channelOperationalFields[name]; ok {
			return true
		}
		_, ok := channelReadOnlyFields[name]
		return ok
	}

	var collect func(rt reflect.Type) []string
	collect = func(rt reflect.Type) []string {
		var names []string
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			if field.Anonymous && field.Type.Kind() == reflect.Struct {
				names = append(names, collect(field.Type)...)
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			names = append(names, name)
		}
		return names
	}

	for _, name := range collect(reflect.TypeOf(PatchChannel{})) {
		assert.Truef(t, classified(name),
			"channel field %q is not classified; add it to channelSensitiveFields, channelNonSensitiveFields, channelOperationalFields, or channelReadOnlyFields in channel_authz.go", name)
	}
}
