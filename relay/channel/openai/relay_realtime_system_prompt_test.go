package openai

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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

// TestRewriteRealtimeSystemPromptPreservesSessionFields protects the session.update wire contract.
func TestRewriteRealtimeSystemPromptPreservesSessionFields(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	oldPass := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	require.NoError(t, model_setting.SetSystemPrompt(`{"models":{"realtime-model":"global prompt"}}`))
	t.Cleanup(func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = oldPass
		require.NoError(t, model_setting.SetSystemPrompt(`{"models":{}}`))
	})
	info := &relaycommon.RelayInfo{
		RequestedModelName: "realtime-model",
		ChannelMeta:        &relaycommon.ChannelMeta{},
	}
	raw := []byte(`{"type":"session.update","session":{"type":"realtime","instructions":"client prompt","output_modalities":["text"],"max_output_tokens":128,"audio":{"output":{"voice":"marin"}}}}`)
	var event dto.RealtimeEvent
	require.NoError(t, common.Unmarshal(raw, &event))
	updated, err := rewriteRealtimeSystemPrompt(c, info, &event, raw)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"session.update","session":{"type":"realtime","instructions":"global prompt\nclient prompt","output_modalities":["text"],"max_output_tokens":128,"audio":{"output":{"voice":"marin"}}}}`, string(updated))
	assert.Equal(t, "global_model_requested", c.GetString(string(constant.ContextKeySystemPromptSource)))

	again, err := rewriteRealtimeSystemPrompt(c, info, &event, updated)
	require.NoError(t, err)
	assert.Equal(t, updated, again)

	info.ChannelSetting.PassThroughBodyEnabled = true
	require.NoError(t, common.Unmarshal(raw, &event))
	passthrough, err := rewriteRealtimeSystemPrompt(c, info, &event, raw)
	require.NoError(t, err)
	assert.Equal(t, raw, passthrough)
}
