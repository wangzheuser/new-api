package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAuthoritativeAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	previousGroups := setting.UserUsableGroups2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":""}`))
	previousDB := model.DB

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.UserSubscription{}))
	model.DB = db

	t.Cleanup(func() {
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		common.RedisEnabled = previousRedisEnabled
		_ = setting.UpdateUserUsableGroupsByJSONString(previousGroups)
		model.DB = previousDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestUserAuthRejectsDatabaseDisabledUserWithEnabledSession(t *testing.T) {
	db := setupAuthoritativeAuthTestDB(t)
	user := &model.User{
		Username: "disabled-session-user",
		Password: "password",
		Status:   common.UserStatusDisabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "disabled-session-aff",
	}
	require.NoError(t, db.Create(user).Error)

	handlerCalled := false
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("authoritative-auth-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", user.Username)
		session.Set("role", user.Role)
		session.Set("id", user.Id)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "国模")
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.GET("/protected", UserAuth(), func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})

	loginRecorder := httptest.NewRecorder()
	router.ServeHTTP(loginRecorder, httptest.NewRequest(http.MethodGet, "/login", nil))
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("New-Api-User", fmt.Sprintf("%d", user.Id))
	for _, sessionCookie := range loginRecorder.Result().Cookies() {
		request.AddCookie(sessionCookie)
	}
	router.ServeHTTP(recorder, request)

	assert.False(t, handlerCalled)
	assert.Equal(t, http.StatusOK, recorder.Code)
}
