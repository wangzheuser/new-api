package operation_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateMultiKeyAutoDisableSetting(t *testing.T) {
	tests := []struct {
		name       string
		temporary  string
		persistent string
		minutes    int
		want       MultiKeyAutoDisableSetting
		wantError  bool
	}{
		{
			name:       "normalizes ranges",
			temporary:  "429, 500-501,501",
			persistent: "401,403",
			minutes:    15,
			want: MultiKeyAutoDisableSetting{
				TemporaryStatusCodes:    "429,500-501",
				PersistentStatusCodes:   "401,403",
				TemporaryDisableMinutes: 15,
			},
		},
		{
			name:       "allows empty temporary list",
			temporary:  "",
			persistent: "401",
			minutes:    10,
			want: MultiKeyAutoDisableSetting{
				TemporaryStatusCodes:    "",
				PersistentStatusCodes:   "401",
				TemporaryDisableMinutes: 10,
			},
		},
		{
			name:       "allows empty persistent list",
			temporary:  "429",
			persistent: "",
			minutes:    10,
			want: MultiKeyAutoDisableSetting{
				TemporaryStatusCodes:    "429",
				PersistentStatusCodes:   "",
				TemporaryDisableMinutes: 10,
			},
		},
		{name: "rejects overlap", temporary: "429-431", persistent: "431", minutes: 10, wantError: true},
		{name: "rejects invalid status", temporary: "99", persistent: "401", minutes: 10, wantError: true},
		{name: "rejects short cooldown", temporary: "429", persistent: "401", minutes: 0, wantError: true},
		{name: "rejects long cooldown", temporary: "429", persistent: "401", minutes: 1441, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateMultiKeyAutoDisableSetting(test.temporary, test.persistent, test.minutes)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestGetMultiKeyAutoDisableSettingFallsBackForInvalidState(t *testing.T) {
	previous := multiKeyAutoDisableSetting
	t.Cleanup(func() {
		multiKeyAutoDisableSetting = previous
	})
	multiKeyAutoDisableSetting = MultiKeyAutoDisableSetting{
		TemporaryStatusCodes:    "429",
		PersistentStatusCodes:   "429",
		TemporaryDisableMinutes: 0,
	}

	assert.Equal(t, defaultMultiKeyAutoDisableSetting, GetMultiKeyAutoDisableSetting())
}

func TestNormalizeMultiKeyAutoDisableOption(t *testing.T) {
	previous := multiKeyAutoDisableSetting
	t.Cleanup(func() {
		multiKeyAutoDisableSetting = previous
	})
	multiKeyAutoDisableSetting = defaultMultiKeyAutoDisableSetting

	normalized, err := NormalizeMultiKeyAutoDisableOption(
		"multi_key_auto_disable_setting.temporary_status_codes",
		"429, 500-501,501",
	)
	require.NoError(t, err)
	assert.Equal(t, "429,500-501", normalized)

	_, err = NormalizeMultiKeyAutoDisableOption(
		"multi_key_auto_disable_setting.temporary_status_codes",
		"401",
	)
	assert.ErrorContains(t, err, "must not overlap")

	_, err = NormalizeMultiKeyAutoDisableOption(
		"multi_key_auto_disable_setting.temporary_disable_minutes",
		"1441",
	)
	assert.ErrorContains(t, err, "between 1 and 1440")
}
