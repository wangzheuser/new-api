package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelAutoDisableDefaultsAndValidation(t *testing.T) {
	setting := GetChannelAutoDisableSetting()
	assert.Equal(t, "408,500-599", setting.StatusCodes)
	assert.Equal(t, 20, setting.SampleSize)
	assert.Equal(t, 3, setting.MinimumSampleSize)
	assert.Equal(t, 80, setting.ErrorRatePercent)
	assert.Equal(t, 10, setting.DisableMinutes)
	assert.False(t, ShouldCountChannelAutoDisableStatusCode(429))
	assert.True(t, ShouldCountChannelAutoDisableStatusCode(503))
	assert.True(t, ShouldCountChannelAutoDisableStatusCode(408))

	normalized, err := NormalizeChannelAutoDisableOption("channel_auto_disable_setting.status_codes", "500-599, 400-499")
	require.NoError(t, err)
	assert.Equal(t, "400-599", normalized)

	_, err = NormalizeChannelAutoDisableOption("channel_auto_disable_setting.sample_size", "1001")
	assert.Error(t, err)
	_, err = NormalizeChannelAutoDisableOption("channel_auto_disable_setting.minimum_sample_size", "0")
	assert.Error(t, err)
	assert.Error(t, ValidateChannelAutoDisableOption("channel_auto_disable_setting.sample_size", "2"))
	assert.Error(t, ValidateChannelAutoDisableOption("channel_auto_disable_setting.minimum_sample_size", "0"))
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
	assert.Equal(t, "408,500-599", setting.StatusCodes)
	assert.Equal(t, 20, setting.SampleSize)
	assert.Equal(t, 3, setting.MinimumSampleSize)
	assert.Equal(t, 80, setting.ErrorRatePercent)
	assert.Equal(t, 10, setting.DisableMinutes)
}

func TestChannelAutoDisableInvalidThresholdPairIsNormalized(t *testing.T) {
	original := channelAutoDisableSetting
	t.Cleanup(func() { channelAutoDisableSetting = original })
	channelAutoDisableSetting = ChannelAutoDisableSetting{StatusCodes: "500", SampleSize: 2, MinimumSampleSize: 3, ErrorRatePercent: 80, DisableMinutes: 10}
	setting := GetChannelAutoDisableSetting()
	assert.Equal(t, 2, setting.SampleSize)
	assert.Equal(t, 2, setting.MinimumSampleSize)
}
