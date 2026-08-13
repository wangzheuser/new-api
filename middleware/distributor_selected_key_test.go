package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSelectedKeyTestContext constructs the minimal request context used by channel setup.
func newSelectedKeyTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// newSelectedKeyTestChannel returns a multi-key channel with a disabled middle key.
func newSelectedKeyTestChannel() *model.Channel {
	return &model.Channel{
		Id:     41,
		Type:   constant.ChannelTypeOpenAI,
		Name:   "multi-key-test",
		Key:    "key-one\nkey-two\nkey-three",
		Models: "gpt-4o-mini",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:           true,
			MultiKeyPollingIndex: 2,
			MultiKeyStatusList: map[int]int{
				1: common.ChannelStatusManuallyDisabled,
			},
		},
	}
}

func TestSetupContextForSelectedChannelKeySelectsDisabledKeyWithoutAdvancingPolling(t *testing.T) {
	channel := newSelectedKeyTestChannel()
	c := newSelectedKeyTestContext()

	apiErr := SetupContextForSelectedChannelKey(c, channel, "gpt-4o-mini", 1)

	require.Nil(t, apiErr)
	assert.Equal(t, "key-two", common.GetContextKeyString(c, constant.ContextKeyChannelKey))
	assert.Equal(t, 1, common.GetContextKeyInt(c, constant.ContextKeyChannelMultiKeyIndex))
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey))
	assert.Equal(t, 2, channel.ChannelInfo.MultiKeyPollingIndex)
}

func TestSetupContextForSelectedChannelKeyValidatesTarget(t *testing.T) {
	tests := []struct {
		name     string
		channel  *model.Channel
		keyIndex int
		message  string
	}{
		{name: "negative index", channel: newSelectedKeyTestChannel(), keyIndex: -1, message: "out of range"},
		{name: "index past end", channel: newSelectedKeyTestChannel(), keyIndex: 3, message: "out of range"},
		{name: "single key channel", channel: &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "key-one", Models: "gpt-4o-mini"}, keyIndex: 0, message: "only supported for multi-key channels"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			apiErr := SetupContextForSelectedChannelKey(newSelectedKeyTestContext(), test.channel, "gpt-4o-mini", test.keyIndex)

			require.NotNil(t, apiErr)
			assert.Contains(t, apiErr.Error(), test.message)
		})
	}
}
