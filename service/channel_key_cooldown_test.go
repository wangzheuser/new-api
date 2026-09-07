package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultiKeyPolicyScopesAndRecovery exercises public upstream semantics, not final error mappings.
func TestMultiKeyPolicyScopesAndRecovery(t *testing.T) {
	setupMultiKeyHealthRedis(t)
	channel := &model.Channel{AutoBan: common.GetPointer(1), ChannelInfo: model.ChannelInfo{IsMultiKey: true}}
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name                             string
		status                           int
		message, header, scope, category string
		action                           MultiKeyFailureAction
		delay                            int64
	}{
		{"credential", 401, "invalid key", "", "key", "credential", MultiKeyFailurePersistent, 0},
		{"old keyword ignored", 403, "Permission denied usage limit", "", "key", "", MultiKeyFailureNone, 0},
		{"model quota", 429, "Error 429: Daily free limit reached on model z-ai/glm-5.3-flash. Try again in 2h 16m", "60", "model", "daily_quota", MultiKeyFailureTemporary, 8160},
		{"later header", 429, "Error 429: Daily free limit reached on model z-ai/glm-5.3-flash. Try again in 2h 16m", "9000", "model", "daily_quota", MultiKeyFailureTemporary, 9000},
		{"workspace", 429, "Workspace allocated quota exceeded, please increase your quota limit.", "", "key", "account_quota", MultiKeyFailureTemporary, 0},
		{"entitlement", 403, "token plan entitlement exhausted", "", "key", "account_quota", MultiKeyFailureTemporary, 0},
		{"generic quota text", 403, "quota exceeded", "", "key", "", MultiKeyFailureNone, 0},
		{"http date", 429, "limited", now.Add(time.Hour).Format(http.TimeFormat), "key", "rate_limit", MultiKeyFailureTemporary, 3600},
		{"invalid delay", 429, "limited", "999999999999999999999", "key", "rate_limit", MultiKeyFailureTemporary, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := upstreamStatusError(test.status, test.message)
			types.ErrOptionWithUpstreamRetryAfter(test.header)(err)
			d := DecideMultiKeyFailure(channel, "z-ai/glm-5.3-flash", err, now)
			assert.Equal(t, test.action, d.Action)
			assert.Equal(t, test.scope, d.Scope)
			assert.Equal(t, test.category, d.Category)
			if test.delay > 0 {
				assert.Equal(t, now.Unix()+test.delay, d.RecoverAt)
			} else {
				assert.Zero(t, d.RecoverAt)
			}
		})
	}
	local := types.NewOpenAIError(errors.New("token plan entitlement exhausted"), types.ErrorCodeBadResponse, 429)
	assert.Equal(t, MultiKeyFailureNone, DecideMultiKeyFailure(channel, "model", local, now).Action)
	channel.OtherSettings = `{"multi_key_auto_disable_override":{"persistent_status_codes":"403","temporary_status_codes":"429","temporary_disable_minutes":10}}`
	assert.Equal(t, MultiKeyFailurePersistent, DecideMultiKeyFailure(channel, "model", upstreamStatusError(403, "token plan entitlement exhausted"), now).Action)
}

// TestMultiKeyProbeLeaseLifecycle exercises concurrent instances, renewal, expiry and cancellation.
func TestMultiKeyProbeLeaseLifecycle(t *testing.T) {
	server := setupMultiKeyHealthRedis(t)
	db := setupChannelSelectProtocolTestDB(t)
	channel := createMultiKeyHealthChannel(t, db, constant.MultiKeyModeRandom)
	name := multiKeyModelDisableKey(channel.Id, "KEY_A", "MODEL_A")
	raw := `{"scope":"model","model":"MODEL_A","disabled_until":1,"version":"v1"}`
	require.NoError(t, common.RDB.Set(context.Background(), name, raw, 24*time.Hour).Err())
	var wg sync.WaitGroup
	results := make(chan *gin.Context, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			_, _, apiErr := SelectChannelKeyForRequest(c, channel, "MODEL_A", map[string]struct{}{MultiKeyFingerprint("KEY_B"): {}})
			if apiErr == nil {
				results <- c
			}
		}()
	}
	wg.Wait()
	close(results)
	require.Len(t, results, 1, "one isolation item admits exactly one simultaneous probe")
	c := <-results
	t.Cleanup(func() { FinishChannelKeyProbe(c, false) })
	token, err := server.Get(name + ":probe")
	require.NoError(t, err)
	server.FastForward(40 * time.Second)
	result, err := common.RDB.Eval(context.Background(), renewKeyProbe, []string{name + ":probe"}, token).Int()
	require.NoError(t, err)
	assert.Equal(t, 1, result)
	assert.Equal(t, 60*time.Second, server.TTL(name+":probe"))
	result, err = common.RDB.Eval(context.Background(), renewKeyProbe, []string{name + ":probe"}, "other-owner").Int()
	require.NoError(t, err)
	assert.Zero(t, result)
	FinishChannelKeyProbe(c, false)
	assert.True(t, server.Exists(name), "unsuccessful probes retain failure history")
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	_, _, apiErr := SelectChannelKeyForRequest(c, channel, "MODEL_A", map[string]struct{}{MultiKeyFingerprint("KEY_B"): {}})
	require.Nil(t, apiErr)
	probe := c.MustGet(string(constant.ContextKeyChannelKeyProbe)).(*channelKeyProbe)
	cancel()
	<-probe.done
	assert.False(t, server.Exists(name+":probe"), "cancellation releases the owned lease")
	FinishChannelKeyProbe(c, false)
	require.NoError(t, server.Set(name+":probe", "terminated-process"))
	server.SetTTL(name+":probe", 60*time.Second)
	server.FastForward(61 * time.Second)
	assert.False(t, server.Exists(name+":probe"))
	assert.True(t, server.Exists(name))
}

// TestMultiKeyModelIsolationAndHalfOpen verifies one recovery lease across independent request contexts.
func TestMultiKeyModelIsolationAndHalfOpen(t *testing.T) {
	server := setupMultiKeyHealthRedis(t)
	db := setupChannelSelectProtocolTestDB(t)
	channel := createMultiKeyHealthChannel(t, db, constant.MultiKeyModeRandom)
	keyName := multiKeyModelDisableKey(channel.Id, "KEY_A", "MODEL_A")
	info := dto.MultiKeyTemporaryDisableInfo{Scope: "model", Model: "MODEL_A", DisabledUntil: time.Now().Add(time.Hour).Unix(), Version: "v1", Failures: 1}
	raw, err := common.Marshal(info)
	require.NoError(t, err)
	require.NoError(t, common.RDB.Set(context.Background(), keyName, raw, 25*time.Hour).Err())
	excluded := map[string]struct{}{MultiKeyFingerprint("KEY_B"): {}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	_, _, apiErr := SelectChannelKeyForRequest(c, channel, "MODEL_A", excluded)
	require.NotNil(t, apiErr)
	key, _, apiErr := SelectChannelKeyForRequest(c, channel, "MODEL_B", excluded)
	require.Nil(t, apiErr)
	assert.Equal(t, "KEY_A", key)
	info.DisabledUntil = time.Now().Add(-time.Second).Unix()
	raw, err = common.Marshal(info)
	require.NoError(t, err)
	require.NoError(t, common.RDB.Set(context.Background(), keyName, raw, time.Hour).Err())
	_, _, apiErr = SelectChannelKeyForRequest(c, channel, "MODEL_A", excluded)
	require.Nil(t, apiErr)
	t.Cleanup(func() { FinishChannelKeyProbe(c, false) })
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	_, _, apiErr = SelectChannelKeyForRequest(c2, channel, "MODEL_A", excluded)
	require.NotNil(t, apiErr)
	assert.True(t, server.Exists(keyName+":probe"))
	FinishChannelKeyProbe(c, true)
	assert.False(t, server.Exists(keyName))
	assert.False(t, server.Exists(keyName+":probe"))
	// A late successful attempt must not clear a newer failure version.
	require.NoError(t, common.RDB.Set(context.Background(), keyName, raw, time.Hour).Err())
	_, _, apiErr = SelectChannelKeyForRequest(c, channel, "MODEL_A", excluded)
	require.Nil(t, apiErr)
	info.Version = "v2"
	info.DisabledUntil = time.Now().Add(time.Hour).Unix()
	raw, err = common.Marshal(info)
	require.NoError(t, err)
	require.NoError(t, common.RDB.Set(context.Background(), keyName, raw, time.Hour).Err())
	FinishChannelKeyProbe(c, true)
	assert.True(t, server.Exists(keyName))
	require.NoError(t, ClearMultiKeyModelCooldown(channel.Id, "KEY_A", "MODEL_A"))
	assert.False(t, server.Exists(keyName))
}

// TestMultiKeyCooldownBackoff uses fixed clock and jitter inputs against the production atomic update.
func TestMultiKeyCooldownBackoff(t *testing.T) {
	server := setupMultiKeyHealthRedis(t)
	now := int64(1000000)
	name := "newapi:multi-key-disable:{1}:key:test"
	for _, want := range []int64{600, 1200, 2400} {
		at, err := common.RDB.Eval(context.Background(), recordKeyCooldown, []string{name}, `{"version":"test"}`, now, 600, 0, 0).Int64()
		require.NoError(t, err)
		assert.Equal(t, now+want, at)
		assert.Equal(t, time.Duration(want+86400)*time.Second, server.TTL(name))
	}
	at, err := common.RDB.Eval(context.Background(), recordKeyCooldown, []string{name}, `{"version":"new"}`, now, 600, now+10000, 0).Int64()
	require.NoError(t, err)
	assert.Equal(t, now+10000, at)
	at, err = common.RDB.Eval(context.Background(), recordKeyCooldown, []string{name}, `{"version":"late"}`, now, 600, now+10, 0).Int64()
	require.NoError(t, err)
	assert.Equal(t, now+10000, at, "concurrent failures cannot shorten the known recovery time")
}

// TestMultiKeyRetryAfterProvenance survives error parsing, mapping and task wrapping.
func TestMultiKeyRetryAfterProvenance(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Retry-After", "120")
	w.WriteHeader(429)
	_, err := w.WriteString(`{"error":{"code":"insufficient_quota","message":"exhausted"}}`)
	require.NoError(t, err)
	apiErr := RelayErrorHandler(context.Background(), w.Result(), false)
	ResetStatusCode(apiErr, `{"429":503}`)
	assert.Equal(t, 503, apiErr.StatusCode)
	status, ok := apiErr.GetUpstreamStatusCode()
	require.True(t, ok)
	assert.Equal(t, 429, status)
	assert.Equal(t, "120", TaskErrorWrapper(apiErr, "upstream", 503).UpstreamError.GetUpstreamRetryAfter())
}

// TestMultiKeyStaleFailureIdentity ensures old requests never disable a replacement key or manual state.
func TestMultiKeyStaleFailureIdentity(t *testing.T) {
	setupMultiKeyHealthRedis(t)
	db := setupChannelSelectProtocolTestDB(t)
	for _, test := range []struct {
		name, currentKeys string
		wantIndex         int
		manual            bool
	}{
		{"reordered", "KEY_B\nKEY_A", 1, false},
		{"deleted", "KEY_B", -1, false},
		{"replaced", "KEY_C\nKEY_B", -1, false},
		{"manual", "KEY_A\nKEY_B", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			channel := createMultiKeyHealthChannel(t, db, constant.MultiKeyModeRandom)
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", test.currentKeys).Error)
			if test.manual {
				require.True(t, model.UpdateChannelStatus(channel.Id, "KEY_A", common.ChannelStatusManuallyDisabled, "operator"))
			}
			_, handled := HandleMultiKeyFailure(channel, 0, "KEY_A", upstreamStatusError(401, "invalid credential"))
			require.True(t, handled)
			loaded, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			if test.wantIndex < 0 {
				assert.Empty(t, loaded.ChannelInfo.MultiKeyStatusList)
			} else if test.manual {
				assert.Equal(t, common.ChannelStatusManuallyDisabled, loaded.ChannelInfo.MultiKeyStatusList[test.wantIndex])
				assert.Equal(t, "operator", loaded.ChannelInfo.MultiKeyDisabledReason[test.wantIndex])
			} else {
				assert.Equal(t, common.ChannelStatusAutoDisabled, loaded.ChannelInfo.MultiKeyStatusList[test.wantIndex])
				assert.NotContains(t, loaded.ChannelInfo.MultiKeyStatusList, 0)
			}
		})
	}
}
