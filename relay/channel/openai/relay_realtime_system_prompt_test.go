package openai

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyRealtimeSystemPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	settings := model_setting.GetGlobalSettings()
	passThrough := settings.PassThroughRequestEnabled
	settings.PassThroughRequestEnabled = false
	t.Cleanup(func() { settings.PassThroughRequestEnabled = passThrough })

	info := &relaycommon.RelayInfo{
		RequestedModelName: "realtime-model",
		ChannelMeta: &relaycommon.ChannelMeta{ChannelSetting: dto.ChannelSettings{
			ModelSystemPrompts: map[string]string{"realtime-model": "model prompt"},
		}},
	}
	event := &dto.RealtimeEvent{
		Type:    dto.RealtimeEventTypeSessionUpdate,
		Session: &dto.RealtimeSession{Instructions: "client prompt"},
	}

	applied := applyRealtimeSystemPrompt(c, info, event)

	require.True(t, applied)
	assert.Equal(t, "model prompt\nclient prompt", event.Session.Instructions)
	assert.True(t, c.GetBool(string(constant.ContextKeySystemPromptApplied)))
	assert.True(t, c.GetBool(string(constant.ContextKeySystemPromptOverride)))

	secondApply := applyRealtimeSystemPrompt(c, info, event)
	assert.False(t, secondApply)
	assert.Equal(t, "model prompt\nclient prompt", event.Session.Instructions)
}
