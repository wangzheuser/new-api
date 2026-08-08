package controller

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type userImpersonationAPIResponse struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Ticket      string `json:"ticket"`
		ExpiresAt   int64  `json:"expires_at"`
		Id          int    `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        int    `json:"role"`
		Status      int    `json:"status"`
		Group       string `json:"group"`
	} `json:"data"`
}

type userImpersonationTestFixture struct {
	db          *gorm.DB
	redisServer *miniredis.Miniredis
	server      *httptest.Server
	admin       *model.User
	target      *model.User
	adminClient *http.Client
}

// setupUserImpersonationTestFixture creates isolated database, Redis, session, and HTTP state.
func setupUserImpersonationTestFixture(t *testing.T) *userImpersonationTestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB := model.DB
	previousLogDB := model.LOG_DB
	previousRDB := common.RDB
	previousRedisEnabled := common.RedisEnabled
	previousSyncFrequency := common.SyncFrequency
	previousMainDBType := common.MainDatabaseType()
	previousLogDBType := common.LogDatabaseType()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Log{},
		&model.UserSubscription{},
		&model.UserGroupGrant{},
	))
	model.DB = db
	model.LOG_DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	redisServer, err := miniredis.Run()
	require.NoError(t, err)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	common.RDB = redisClient
	common.RedisEnabled = true
	common.SyncFrequency = 60

	admin := seedUserImpersonationUser(t, db, "imp-admin", common.RoleAdminUser, common.UserStatusEnabled)
	admin.LastLoginAt = 111
	require.NoError(t, db.Model(admin).Update("last_login_at", admin.LastLoginAt).Error)
	target := seedUserImpersonationUser(t, db, "imp-target", common.RoleCommonUser, common.UserStatusEnabled)
	target.LastLoginAt = 222
	require.NoError(t, db.Model(target).Update("last_login_at", target.LastLoginAt).Error)
	// Preload the existing user cache path so audit lookups stay synchronous in the fixture.
	require.NoError(t, common.RedisHSetObj(fmt.Sprintf("user:%d", admin.Id), admin.ToBaseUser(), time.Minute))
	require.NoError(t, common.RedisHSetObj(fmt.Sprintf("user:%d", target.Id), target.ToBaseUser(), time.Minute))

	router := gin.New()
	store := cookie.NewStore([]byte("user-impersonation-test-session-secret"))
	router.Use(sessions.Sessions("session", store))
	router.GET("/login/:id", func(c *gin.Context) {
		var user model.User
		id := c.Param("id")
		if err := db.First(&user, "id = ?", id).Error; err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		session := sessions.Default(c)
		session.Set("id", user.Id)
		session.Set("username", user.Username)
		session.Set("role", user.Role)
		session.Set("status", user.Status)
		session.Set("group", user.Group)
		if err := session.Save(); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	router.POST("/issue/:id", middleware.AdminAuth(), CreateUserImpersonationTicket)
	router.POST("/redeem", RedeemUserImpersonationTicket)
	router.GET("/self", middleware.UserAuth(), GetSelf)
	server := httptest.NewServer(router)
	adminClient := newUserImpersonationHTTPClient(t)
	loginResponse, err := adminClient.Get(fmt.Sprintf("%s/login/%d", server.URL, admin.Id))
	require.NoError(t, err)
	require.NoError(t, loginResponse.Body.Close())
	require.Equal(t, http.StatusNoContent, loginResponse.StatusCode)

	t.Cleanup(func() {
		server.Close()
		_ = redisClient.Close()
		redisServer.Close()
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		model.DB = previousDB
		model.LOG_DB = previousLogDB
		common.RDB = previousRDB
		common.RedisEnabled = previousRedisEnabled
		common.SyncFrequency = previousSyncFrequency
		common.SetDatabaseTypes(previousMainDBType, previousLogDBType)
	})

	return &userImpersonationTestFixture{
		db:          db,
		redisServer: redisServer,
		server:      server,
		admin:       admin,
		target:      target,
		adminClient: adminClient,
	}
}

// seedUserImpersonationUser inserts one explicit authorization target.
func seedUserImpersonationUser(t *testing.T, db *gorm.DB, username string, role int, status int) *model.User {
	t.Helper()
	user := &model.User{
		Username:    username,
		Password:    "password",
		DisplayName: username + " display",
		Role:        role,
		Status:      status,
		Group:       "default",
		AffCode:     username + "-aff",
		Setting:     "{}",
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

// newUserImpersonationHTTPClient returns an isolated browser cookie jar.
func newUserImpersonationHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar}
}

// doUserImpersonationRequest sends JSON and decodes the common API envelope.
func doUserImpersonationRequest(client *http.Client, method string, url string, body any, userId int) (userImpersonationAPIResponse, *http.Response, error) {
	var requestBody io.Reader
	if body != nil {
		data, err := common.Marshal(body)
		if err != nil {
			return userImpersonationAPIResponse{}, nil, err
		}
		requestBody = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		return userImpersonationAPIResponse{}, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if userId > 0 {
		request.Header.Set("New-Api-User", fmt.Sprintf("%d", userId))
	}

	response, err := client.Do(request)
	if err != nil {
		return userImpersonationAPIResponse{}, nil, err
	}
	data, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil {
		return userImpersonationAPIResponse{}, response, readErr
	}
	if closeErr != nil {
		return userImpersonationAPIResponse{}, response, closeErr
	}
	var result userImpersonationAPIResponse
	if len(data) > 0 {
		if err := common.Unmarshal(data, &result); err != nil {
			return userImpersonationAPIResponse{}, response, err
		}
	}
	return result, response, nil
}

// issueUserImpersonationTicket signs one ticket through the authenticated administrator route.
func (fixture *userImpersonationTestFixture) issueUserImpersonationTicket(t *testing.T, targetUserId int) userImpersonationAPIResponse {
	t.Helper()
	result, _, err := doUserImpersonationRequest(
		fixture.adminClient,
		http.MethodPost,
		fmt.Sprintf("%s/issue/%d", fixture.server.URL, targetUserId),
		nil,
		fixture.admin.Id,
	)
	require.NoError(t, err)
	return result
}

// redeemUserImpersonationTicketForTest redeems through an isolated browser client.
func (fixture *userImpersonationTestFixture) redeemUserImpersonationTicketForTest(client *http.Client, ticket string) (userImpersonationAPIResponse, *http.Response, error) {
	return doUserImpersonationRequest(client, http.MethodPost, fixture.server.URL+"/redeem", map[string]string{"ticket": ticket}, 0)
}

func TestCreateUserImpersonationTicketStoresHashedFiveMinuteTicket(t *testing.T) {
	fixture := setupUserImpersonationTestFixture(t)

	result := fixture.issueUserImpersonationTicket(t, fixture.target.Id)
	require.True(t, result.Success)
	require.Len(t, result.Data.Ticket, 48)
	assert.WithinDuration(t, time.Now().Add(5*time.Minute), time.Unix(result.Data.ExpiresAt, 0), 2*time.Second)

	ticketDigest := sha256.Sum256([]byte(result.Data.Ticket))
	redisKey := fmt.Sprintf("user:impersonation:%x", ticketDigest)
	assert.True(t, fixture.redisServer.Exists(redisKey))
	assert.NotContains(t, redisKey, result.Data.Ticket)
	assert.Equal(t, userImpersonationTicketTTL, fixture.redisServer.TTL(redisKey))

	storedValue, err := fixture.redisServer.Get(redisKey)
	require.NoError(t, err)
	var storedMap map[string]any
	require.NoError(t, common.UnmarshalJsonStr(storedValue, &storedMap))
	assert.Len(t, storedMap, 3)
	assert.EqualValues(t, fixture.admin.Id, storedMap["admin_user_id"])
	assert.EqualValues(t, fixture.target.Id, storedMap["target_user_id"])
	assert.NotZero(t, storedMap["issued_at"])
}

func TestCreateUserImpersonationTicketRejectsIneligibleTargets(t *testing.T) {
	fixture := setupUserImpersonationTestFixture(t)
	adminTarget := seedUserImpersonationUser(t, fixture.db, "target-admin", common.RoleAdminUser, common.UserStatusEnabled)
	rootTarget := seedUserImpersonationUser(t, fixture.db, "target-root", common.RoleRootUser, common.UserStatusEnabled)
	disabledTarget := seedUserImpersonationUser(t, fixture.db, "target-off", common.RoleCommonUser, common.UserStatusDisabled)
	deletedTarget := seedUserImpersonationUser(t, fixture.db, "target-gone", common.RoleCommonUser, common.UserStatusEnabled)
	require.NoError(t, fixture.db.Delete(deletedTarget).Error)

	tests := []struct {
		name     string
		targetId int
	}{
		{name: "administrator", targetId: adminTarget.Id},
		{name: "root", targetId: rootTarget.Id},
		{name: "disabled", targetId: disabledTarget.Id},
		{name: "deleted", targetId: deletedTarget.Id},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := fixture.issueUserImpersonationTicket(t, test.targetId)
			assert.False(t, result.Success)
		})
	}
}

func TestRedeemUserImpersonationTicketCreatesSessionWithoutLoginSideEffects(t *testing.T) {
	fixture := setupUserImpersonationTestFixture(t)
	issued := fixture.issueUserImpersonationTicket(t, fixture.target.Id)
	require.True(t, issued.Success)
	ticketHash := fmt.Sprintf("%x", sha256.Sum256([]byte(issued.Data.Ticket)))

	client := newUserImpersonationHTTPClient(t)
	redeemed, response, err := fixture.redeemUserImpersonationTicketForTest(client, issued.Data.Ticket)
	require.NoError(t, err)
	require.True(t, redeemed.Success)
	assert.Equal(t, fixture.target.Id, redeemed.Data.Id)
	assert.Equal(t, fixture.target.Username, redeemed.Data.Username)
	assert.Equal(t, common.RoleCommonUser, redeemed.Data.Role)
	assert.NotEmpty(t, response.Cookies())

	self, _, err := doUserImpersonationRequest(client, http.MethodGet, fixture.server.URL+"/self", nil, fixture.target.Id)
	require.NoError(t, err)
	require.True(t, self.Success)
	assert.Equal(t, fixture.target.Id, self.Data.Id)

	second, _, err := fixture.redeemUserImpersonationTicketForTest(newUserImpersonationHTTPClient(t), issued.Data.Ticket)
	require.NoError(t, err)
	assert.False(t, second.Success)
	assert.Equal(t, "impersonation_ticket_invalid", second.Code)

	var refreshedTarget model.User
	require.NoError(t, fixture.db.First(&refreshedTarget, fixture.target.Id).Error)
	assert.Equal(t, fixture.target.LastLoginAt, refreshedTarget.LastLoginAt)

	var loginLogCount int64
	require.NoError(t, fixture.db.Model(&model.Log{}).
		Where("user_id = ? AND type = ?", fixture.target.Id, model.LogTypeLogin).
		Count(&loginLogCount).Error)
	assert.Zero(t, loginLogCount)

	var manageLogs []model.Log
	require.NoError(t, fixture.db.Where(
		"user_id = ? AND type = ? AND (other LIKE ? OR other LIKE ?)",
		fixture.admin.Id,
		model.LogTypeManage,
		`%"action":"user.impersonation_link_create"%`,
		`%"action":"user.impersonation_redeem"%`,
	).
		Order("id asc").Find(&manageLogs).Error)
	require.Len(t, manageLogs, 2)
	actions := make([]string, 0, 2)
	for _, log := range manageLogs {
		assert.NotContains(t, log.Content, issued.Data.Ticket)
		assert.NotContains(t, log.Other, issued.Data.Ticket)
		assert.NotContains(t, log.Other, ticketHash)
		var other struct {
			Op struct {
				Action string `json:"action"`
			} `json:"op"`
		}
		require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
		actions = append(actions, other.Op.Action)
	}
	assert.Equal(t, []string{"user.impersonation_link_create", "user.impersonation_redeem"}, actions)
}

func TestRedeemUserImpersonationTicketExistingSessionDoesNotConsumeTicket(t *testing.T) {
	fixture := setupUserImpersonationTestFixture(t)
	issued := fixture.issueUserImpersonationTicket(t, fixture.target.Id)
	require.True(t, issued.Success)
	redisKey := userImpersonationTicketKey(issued.Data.Ticket)

	rejected, _, err := fixture.redeemUserImpersonationTicketForTest(fixture.adminClient, issued.Data.Ticket)
	require.NoError(t, err)
	assert.False(t, rejected.Success)
	assert.Equal(t, "impersonation_existing_session", rejected.Code)
	assert.True(t, fixture.redisServer.Exists(redisKey))

	redeemed, _, err := fixture.redeemUserImpersonationTicketForTest(newUserImpersonationHTTPClient(t), issued.Data.Ticket)
	require.NoError(t, err)
	assert.True(t, redeemed.Success)
}

func TestRedeemUserImpersonationTicketIsAtomic(t *testing.T) {
	fixture := setupUserImpersonationTestFixture(t)
	issued := fixture.issueUserImpersonationTicket(t, fixture.target.Id)
	require.True(t, issued.Success)

	results := make(chan userImpersonationAPIResponse, 2)
	errors := make(chan error, 2)
	clients := []*http.Client{
		newUserImpersonationHTTPClient(t),
		newUserImpersonationHTTPClient(t),
	}
	var waitGroup sync.WaitGroup
	for _, client := range clients {
		waitGroup.Add(1)
		go func(client *http.Client) {
			defer waitGroup.Done()
			result, _, err := fixture.redeemUserImpersonationTicketForTest(client, issued.Data.Ticket)
			results <- result
			errors <- err
		}(client)
	}
	waitGroup.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}

	successCount := 0
	for result := range results {
		if result.Success {
			successCount++
		}
	}
	assert.Equal(t, 1, successCount)
}

func TestRedeemUserImpersonationTicketExpiresAfterFiveMinutes(t *testing.T) {
	fixture := setupUserImpersonationTestFixture(t)
	issued := fixture.issueUserImpersonationTicket(t, fixture.target.Id)
	require.True(t, issued.Success)
	fixture.redisServer.FastForward(userImpersonationTicketTTL)

	result, _, err := fixture.redeemUserImpersonationTicketForTest(newUserImpersonationHTTPClient(t), issued.Data.Ticket)
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Equal(t, "impersonation_ticket_invalid", result.Code)
}

func TestRedeemUserImpersonationTicketRejectsChangedAuthorizationState(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *userImpersonationTestFixture)
	}{
		{
			name: "administrator disabled",
			change: func(t *testing.T, fixture *userImpersonationTestFixture) {
				require.NoError(t, fixture.db.Model(fixture.admin).Update("status", common.UserStatusDisabled).Error)
			},
		},
		{
			name: "administrator demoted",
			change: func(t *testing.T, fixture *userImpersonationTestFixture) {
				require.NoError(t, fixture.db.Model(fixture.admin).Update("role", common.RoleCommonUser).Error)
			},
		},
		{
			name: "target disabled",
			change: func(t *testing.T, fixture *userImpersonationTestFixture) {
				require.NoError(t, fixture.db.Model(fixture.target).Update("status", common.UserStatusDisabled).Error)
			},
		},
		{
			name: "target deleted",
			change: func(t *testing.T, fixture *userImpersonationTestFixture) {
				require.NoError(t, fixture.db.Delete(fixture.target).Error)
			},
		},
		{
			name: "target promoted",
			change: func(t *testing.T, fixture *userImpersonationTestFixture) {
				require.NoError(t, fixture.db.Model(fixture.target).Update("role", common.RoleAdminUser).Error)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := setupUserImpersonationTestFixture(t)
			issued := fixture.issueUserImpersonationTicket(t, fixture.target.Id)
			require.True(t, issued.Success)
			test.change(t, fixture)

			result, _, err := fixture.redeemUserImpersonationTicketForTest(newUserImpersonationHTTPClient(t), issued.Data.Ticket)
			require.NoError(t, err)
			assert.False(t, result.Success)
			assert.Equal(t, "impersonation_ticket_invalid", result.Code)
			assert.False(t, fixture.redisServer.Exists(userImpersonationTicketKey(issued.Data.Ticket)))
		})
	}
}

func TestRedeemUserImpersonationTicketUsesGenericInvalidResponse(t *testing.T) {
	fixture := setupUserImpersonationTestFixture(t)
	tests := []struct {
		name   string
		ticket string
	}{
		{name: "empty", ticket: ""},
		{name: "blank", ticket: "   "},
		{name: "random", ticket: "not-a-real-ticket"},
		{name: "too long", ticket: strings.Repeat("x", userImpersonationTicketMaxLen+1)},
	}
	var message string
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, _, err := fixture.redeemUserImpersonationTicketForTest(newUserImpersonationHTTPClient(t), test.ticket)
			require.NoError(t, err)
			assert.False(t, result.Success)
			assert.Equal(t, "impersonation_ticket_invalid", result.Code)
			if message == "" {
				message = result.Message
			}
			assert.Equal(t, message, result.Message)
		})
	}
}

func TestCreateUserImpersonationTicketRequiresRedis(t *testing.T) {
	fixture := setupUserImpersonationTestFixture(t)
	common.RedisEnabled = false

	result := fixture.issueUserImpersonationTicket(t, fixture.target.Id)
	assert.False(t, result.Success)
	assert.Equal(t, "impersonation_server_error", result.Code)
}
