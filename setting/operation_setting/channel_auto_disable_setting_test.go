package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelAutoDisableDefaultsAndValidation(t *testing.T) {
	setting := GetChannelAutoDisableSetting()
	assert.Equal(t, "400-599", setting.StatusCodes)
	assert.Equal(t, 10, setting.WindowMinutes)
	assert.Equal(t, 30, setting.MinRequests)
	assert.Equal(t, 80, setting.ErrorRatePercent)
	assert.Equal(t, 10, setting.DisableMinutes)
	assert.True(t, ShouldCountChannelAutoDisableStatusCode(429))
	assert.True(t, ShouldCountChannelAutoDisableStatusCode(503))
	assert.False(t, ShouldCountChannelAutoDisableStatusCode(399))

	normalized, err := NormalizeChannelAutoDisableOption("channel_auto_disable_setting.status_codes", "500-599, 400-499")
	require.NoError(t, err)
	assert.Equal(t, "400-599", normalized)

	_, err = NormalizeChannelAutoDisableOption("channel_auto_disable_setting.window_minutes", "61")
	assert.Error(t, err)
	_, err = NormalizeChannelAutoDisableOption("channel_auto_disable_setting.min_requests", "0")
	assert.Error(t, err)
	_, err = NormalizeChannelAutoDisableOption("channel_auto_disable_setting.error_rate_percent", "101")
	assert.Error(t, err)
	_, err = NormalizeChannelAutoDisableOption("channel_auto_disable_setting.disable_minutes", "0")
	assert.Error(t, err)
}

func TestChannelAutoDisableEmptyValuesFallBackToDefaults(t *testing.T) {
	original := channelAutoDisableSetting
	t.Cleanup(func() { channelAutoDisableSetting = original })
	channelAutoDisableSetting = ChannelAutoDisableSetting{}

	setting := GetChannelAutoDisableSetting()
	assert.Equal(t, "400-599", setting.StatusCodes)
	assert.Equal(t, 10, setting.WindowMinutes)
	assert.Equal(t, 30, setting.MinRequests)
	assert.Equal(t, 80, setting.ErrorRatePercent)
	assert.Equal(t, 10, setting.DisableMinutes)
}
