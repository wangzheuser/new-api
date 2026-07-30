package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetStatusExposesEmailRestrictions verifies that registration clients receive active email restrictions.
func TestGetStatusExposesEmailRestrictions(t *testing.T) {
	originalDomainRestriction := common.EmailDomainRestrictionEnabled
	originalAliasRestriction := common.EmailAliasRestrictionEnabled
	originalWhitelist := common.EmailDomainWhitelist
	t.Cleanup(func() {
		common.EmailDomainRestrictionEnabled = originalDomainRestriction
		common.EmailAliasRestrictionEnabled = originalAliasRestriction
		common.EmailDomainWhitelist = originalWhitelist
	})

	common.EmailDomainRestrictionEnabled = true
	common.EmailAliasRestrictionEnabled = true
	common.EmailDomainWhitelist = []string{"gmail.com", "outlook.com"}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)

	GetStatus(ctx)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			AllowedEmailDomains          []string `json:"allowed_email_domains"`
			EmailAliasRestrictionEnabled bool     `json:"email_alias_restriction_enabled"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, []string{"gmail.com", "outlook.com"}, response.Data.AllowedEmailDomains)
	assert.True(t, response.Data.EmailAliasRestrictionEnabled)
}

// TestSendEmailVerificationRejectsRestrictedAddresses verifies user-facing domain and alias errors.
func TestSendEmailVerificationRejectsRestrictedAddresses(t *testing.T) {
	require.NoError(t, i18n.Init())

	originalDomainRestriction := common.EmailDomainRestrictionEnabled
	originalAliasRestriction := common.EmailAliasRestrictionEnabled
	originalWhitelist := common.EmailDomainWhitelist
	t.Cleanup(func() {
		common.EmailDomainRestrictionEnabled = originalDomainRestriction
		common.EmailAliasRestrictionEnabled = originalAliasRestriction
		common.EmailDomainWhitelist = originalWhitelist
	})

	common.EmailDomainRestrictionEnabled = true
	common.EmailAliasRestrictionEnabled = true
	common.EmailDomainWhitelist = []string{"gmail.com", "outlook.com"}

	tests := []struct {
		name            string
		email           string
		expectedMessage string
	}{
		{
			name:            "domain is not allowed",
			email:           "user@example.com",
			expectedMessage: "Email addresses from @example.com are not supported. Allowed domains: gmail.com, outlook.com",
		},
		{
			name:            "plus alias is not allowed",
			email:           "abc+123@gmail.com",
			expectedMessage: "Email aliases containing '+' are not allowed. Please use the original email address.",
		},
	}

	gin.SetMode(gin.TestMode)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(
				http.MethodGet,
				"/api/verification?email="+url.QueryEscape(test.email),
				nil,
			)

			SendEmailVerification(ctx)

			var response struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Equal(t, test.expectedMessage, response.Message)
		})
	}
}

// TestRegisterRejectsEmailAlias verifies that final registration rechecks alias restrictions.
func TestRegisterRejectsEmailAlias(t *testing.T) {
	require.NoError(t, i18n.Init())

	originalRegisterEnabled := common.RegisterEnabled
	originalPasswordRegisterEnabled := common.PasswordRegisterEnabled
	originalEmailVerificationEnabled := common.EmailVerificationEnabled
	originalDomainRestriction := common.EmailDomainRestrictionEnabled
	originalAliasRestriction := common.EmailAliasRestrictionEnabled
	originalWhitelist := common.EmailDomainWhitelist
	t.Cleanup(func() {
		common.RegisterEnabled = originalRegisterEnabled
		common.PasswordRegisterEnabled = originalPasswordRegisterEnabled
		common.EmailVerificationEnabled = originalEmailVerificationEnabled
		common.EmailDomainRestrictionEnabled = originalDomainRestriction
		common.EmailAliasRestrictionEnabled = originalAliasRestriction
		common.EmailDomainWhitelist = originalWhitelist
	})

	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.EmailVerificationEnabled = true
	common.EmailDomainRestrictionEnabled = true
	common.EmailAliasRestrictionEnabled = true
	common.EmailDomainWhitelist = []string{"gmail.com"}

	body := bytes.NewBufferString(`{
		"username":"new-user",
		"password":"password",
		"email":"abc+123@gmail.com",
		"verification_code":"123456"
	}`)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/register", body)
	ctx.Request.Header.Set("Content-Type", "application/json")

	Register(ctx)

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(
		t,
		"Email aliases containing '+' are not allowed. Please use the original email address.",
		response.Message,
	)
}
