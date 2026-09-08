package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthPolicyRejection 保证拒绝分支有诊断标记，正常鉴权仍进入业务处理器。
func TestAuthPolicyRejection(t *testing.T) {
	for i, tc := range []struct {
		name, group, reason string
		status              int
	}{
		{"allowed", "default", "", http.StatusNoContent},
		{"denied", "restricted-fixture", "auth_group_not_allowed", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupAuthoritativeAuthTestDB(t)
			t.Setenv("LOG_SQL_DSN", "")
			previousLogDB := model.LOG_DB
			require.NoError(t, model.InitLogDB())
			t.Cleanup(func() { model.LOG_DB = previousLogDB })
			user := model.User{Id: 9300 + i, Username: "policy-" + tc.name, Password: "fixture", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default", AffCode: "policy-" + tc.name}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{Id: 9400 + i, UserId: user.Id, Key: fmt.Sprintf("fixturekey%d", i), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, Group: tc.group}
			require.NoError(t, db.Create(&token).Error)
			var reason string
			called := false
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Next(); reason = c.GetString("auth_policy_rejection") })
			r.POST("/v1/messages", TokenAuth(), func(c *gin.Context) { called = true; c.Status(http.StatusNoContent) })
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Equal(t, tc.reason, reason)
			assert.Equal(t, tc.status == http.StatusNoContent, called)
			if tc.status == http.StatusForbidden {
				assert.Contains(t, rec.Body.String(), "无权访问")
				assert.NotContains(t, rec.Body.String(), "auth_group_not_allowed")
			}
		})
	}
}
