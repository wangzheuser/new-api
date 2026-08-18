package controller

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeUserModelRateLimitRuleRequest protects the management API validation contract.
func TestNormalizeUserModelRateLimitRuleRequest(t *testing.T) {
	tests := []struct {
		name    string
		request dto.UserModelRateLimitRuleRequest
		wantErr string
	}{
		{
			name: "valid rule is canonicalized",
			request: dto.UserModelRateLimitRuleRequest{
				UserId:       1001,
				Group:        " vip ",
				TotalCount:   60,
				SuccessCount: 50,
				Response: &dto.ModelRateLimitResponse{
					StatusCode:   403,
					ErrorMessage: " custom response ",
				},
			},
		},
		{
			name:    "user is required",
			request: dto.UserModelRateLimitRuleRequest{Group: "vip", SuccessCount: 1},
			wantErr: "user_id",
		},
		{
			name:    "group is required",
			request: dto.UserModelRateLimitRuleRequest{UserId: 1, Group: " ", SuccessCount: 1},
			wantErr: "group",
		},
		{
			name:    "total count is bounded",
			request: dto.UserModelRateLimitRuleRequest{UserId: 1, Group: "vip", TotalCount: -1, SuccessCount: 1},
			wantErr: "total_count",
		},
		{
			name:    "success count is required",
			request: dto.UserModelRateLimitRuleRequest{UserId: 1, Group: "vip", SuccessCount: 0},
			wantErr: "success_count",
		},
		{
			name: "response status is bounded",
			request: dto.UserModelRateLimitRuleRequest{
				UserId:       1,
				Group:        "vip",
				SuccessCount: 1,
				Response:     &dto.ModelRateLimitResponse{StatusCode: 200, ErrorMessage: "message"},
			},
			wantErr: "status_code",
		},
		{
			name: "response message is bounded",
			request: dto.UserModelRateLimitRuleRequest{
				UserId:       1,
				Group:        "vip",
				SuccessCount: 1,
				Response:     &dto.ModelRateLimitResponse{StatusCode: 429, ErrorMessage: strings.Repeat("x", 513)},
			},
			wantErr: "error_message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := normalizeUserModelRateLimitRuleRequest(tt.request)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "vip", rule.GroupName)
			assert.Equal(t, 60, rule.TotalCount)
			assert.Equal(t, 50, rule.SuccessCount)
			require.NotNil(t, rule.StatusCode)
			require.NotNil(t, rule.ErrorMessage)
			assert.Equal(t, 403, *rule.StatusCode)
			assert.Equal(t, "custom response", *rule.ErrorMessage)
		})
	}
}

// TestBuildUserModelRateLimitRuleView verifies the API exposes the effective response and source.
func TestBuildUserModelRateLimitRuleView(t *testing.T) {
	previous := setting.UserModelRateLimitResponseConfig2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(previous))
	})
	require.NoError(t, setting.UpdateUserModelRateLimitResponseConfigByJSONString(`{"delay_seconds":2,"default_response":{"status_code":429,"error_message":"global"},"group_responses":{"vip":{"status_code":403,"error_message":"group"}}}`))

	rule := model.UserModelRateLimit{
		Id:           12,
		UserId:       1001,
		GroupName:    "vip",
		TotalCount:   60,
		SuccessCount: 50,
		CreatedAt:    100,
		UpdatedAt:    200,
	}
	user := model.User{Id: 1001, Username: "example", DisplayName: "Example", Email: "example@example.com"}
	view := buildUserModelRateLimitRuleViewFromRule(rule, user)
	assert.Nil(t, view.Response)
	assert.Equal(t, "group", view.EffectiveResponse.Source)
	assert.Equal(t, 403, view.EffectiveResponse.StatusCode)
	assert.Equal(t, "group", view.EffectiveResponse.ErrorMessage)

	status := 451
	message := "user response"
	rule.StatusCode = &status
	rule.ErrorMessage = &message
	view = buildUserModelRateLimitRuleViewFromRule(rule, user)
	require.NotNil(t, view.Response)
	assert.Equal(t, "user_group", view.EffectiveResponse.Source)
	assert.Equal(t, 451, view.EffectiveResponse.StatusCode)
	assert.Equal(t, "user response", view.EffectiveResponse.ErrorMessage)
}

// TestSortedUserModelRateLimitGroupResponses keeps configuration API ordering deterministic.
func TestSortedUserModelRateLimitGroupResponses(t *testing.T) {
	items := sortedUserModelRateLimitGroupResponses(map[string]dto.ModelRateLimitResponse{
		"vip":     {StatusCode: 403, ErrorMessage: "vip"},
		"default": {StatusCode: 429, ErrorMessage: "default"},
		"auto":    {StatusCode: 409, ErrorMessage: "auto"},
	})
	require.Len(t, items, 3)
	assert.Equal(t, []string{"auto", "default", "vip"}, []string{items[0].Group, items[1].Group, items[2].Group})

	encoded, err := common.Marshal(items)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"status_code":409`)
}
