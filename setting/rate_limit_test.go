package setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeUserModelRateLimitResponseConfig validates canonical storage and boundary checks.
func TestNormalizeUserModelRateLimitResponseConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  dto.UserModelRateLimitResponseConfig
		wantErr string
	}{
		{
			name: "valid configuration is trimmed",
			config: dto.UserModelRateLimitResponseConfig{
				DelaySeconds:    2,
				DefaultResponse: dto.ModelRateLimitResponse{StatusCode: 429, ErrorMessage: " default message "},
				GroupResponses: map[string]dto.ModelRateLimitResponse{
					" vip ": {StatusCode: 403, ErrorMessage: " group message "},
				},
			},
		},
		{
			name: "delay is bounded",
			config: dto.UserModelRateLimitResponseConfig{
				DelaySeconds:    61,
				DefaultResponse: dto.ModelRateLimitResponse{StatusCode: 429, ErrorMessage: "message"},
			},
			wantErr: "delay_seconds",
		},
		{
			name: "status is bounded",
			config: dto.UserModelRateLimitResponseConfig{
				DefaultResponse: dto.ModelRateLimitResponse{StatusCode: 399, ErrorMessage: "message"},
			},
			wantErr: "status_code",
		},
		{
			name: "message is required",
			config: dto.UserModelRateLimitResponseConfig{
				DefaultResponse: dto.ModelRateLimitResponse{StatusCode: 429, ErrorMessage: "  "},
			},
			wantErr: "error_message",
		},
		{
			name: "message length is bounded",
			config: dto.UserModelRateLimitResponseConfig{
				DefaultResponse: dto.ModelRateLimitResponse{StatusCode: 429, ErrorMessage: strings.Repeat("x", 513)},
			},
			wantErr: "error_message",
		},
		{
			name: "trimmed duplicate group is rejected",
			config: dto.UserModelRateLimitResponseConfig{
				DefaultResponse: dto.ModelRateLimitResponse{StatusCode: 429, ErrorMessage: "message"},
				GroupResponses: map[string]dto.ModelRateLimitResponse{
					"vip":  {StatusCode: 403, ErrorMessage: "one"},
					" vip": {StatusCode: 409, ErrorMessage: "two"},
				},
			},
			wantErr: "duplicate group",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, err := NormalizeUserModelRateLimitResponseConfig(tt.config)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 2, normalized.DelaySeconds)
			assert.Equal(t, "default message", normalized.DefaultResponse.ErrorMessage)
			assert.Equal(t, "group message", normalized.GroupResponses["vip"].ErrorMessage)
		})
	}
}

// TestResolveUserModelRateLimitResponse verifies user, group, and global response priority.
func TestResolveUserModelRateLimitResponse(t *testing.T) {
	previous := UserModelRateLimitResponseConfig2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateUserModelRateLimitResponseConfigByJSONString(previous))
	})

	config := dto.UserModelRateLimitResponseConfig{
		DelaySeconds:    3,
		DefaultResponse: dto.ModelRateLimitResponse{StatusCode: 429, ErrorMessage: "global"},
		GroupResponses: map[string]dto.ModelRateLimitResponse{
			"vip": {StatusCode: 403, ErrorMessage: "group"},
		},
	}
	encoded, err := common.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, UpdateUserModelRateLimitResponseConfigByJSONString(string(encoded)))

	response, source, delay := ResolveUserModelRateLimitResponse("default", nil, nil)
	assert.Equal(t, dto.ModelRateLimitResponse{StatusCode: 429, ErrorMessage: "global"}, response)
	assert.Equal(t, "global", source)
	assert.Equal(t, 3, delay)

	response, source, delay = ResolveUserModelRateLimitResponse("vip", nil, nil)
	assert.Equal(t, dto.ModelRateLimitResponse{StatusCode: 403, ErrorMessage: "group"}, response)
	assert.Equal(t, "group", source)
	assert.Equal(t, 3, delay)

	status := 451
	message := "user"
	response, source, delay = ResolveUserModelRateLimitResponse("vip", &status, &message)
	assert.Equal(t, dto.ModelRateLimitResponse{StatusCode: 451, ErrorMessage: "user"}, response)
	assert.Equal(t, "user_group", source)
	assert.Equal(t, 3, delay)

	invalidStatus := 200
	response, source, _ = ResolveUserModelRateLimitResponse("vip", &invalidStatus, &message)
	assert.Equal(t, dto.ModelRateLimitResponse{StatusCode: 403, ErrorMessage: "group"}, response)
	assert.Equal(t, "group", source)
}

// TestUserModelRateLimitResponseConfigJSON resets malformed persisted values to the built-in response.
func TestUserModelRateLimitResponseConfigJSON(t *testing.T) {
	previous := UserModelRateLimitResponseConfig2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateUserModelRateLimitResponseConfigByJSONString(previous))
	})

	require.Error(t, CheckUserModelRateLimitResponseConfigJSONString(`{"delay_seconds":-1}`))
	require.Error(t, UpdateUserModelRateLimitResponseConfigByJSONString(`not-json`))
	assert.Equal(t, defaultUserModelRateLimitResponseConfig(), GetUserModelRateLimitResponseConfig())
}
