package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	projecttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupContextFallbackTestDB 创建隔离的渠道数据库，用于验证同渠道和显式跨渠道路由。
func setupContextFallbackTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousMemoryCache := common.MemoryCacheEnabled
	dsn := fmt.Sprintf("file:context-fallback-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCache
	})
	return db
}

// newContextFallbackChannel 构造可直接进入 distributor 设置链的测试渠道。
func newContextFallbackChannel(name, models string, settings dto.ChannelSettings) *model.Channel {
	channel := &model.Channel{
		Type:        constant.ChannelTypeOpenAI,
		Key:         "test-key",
		Status:      common.ChannelStatusEnabled,
		Name:        name,
		Models:      models,
		Group:       "default",
		CreatedTime: common.GetTimestamp(),
	}
	channel.SetSetting(settings)
	return channel
}

// newContextFallbackRequest 构造一个提示词注入后稳定越过测试阈值的 Chat 请求。
func newContextFallbackRequest() *dto.GeneralOpenAIRequest {
	maxTokens := uint(8)
	return &dto.GeneralOpenAIRequest{
		Model:     "MODEL_A",
		MaxTokens: &maxTokens,
		Messages: []dto.Message{{
			Role:    "user",
			Content: "short request",
		}},
	}
}

// setupContextFallbackGinContext 使用源渠道初始化一次真实的渠道上下文。
func setupContextFallbackGinContext(t *testing.T, source *model.Channel) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "MODEL_A")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	require.Nil(t, middleware.SetupContextForSelectedChannel(c, source, "MODEL_A"))
	return c
}

func TestPrepareContextFallbackSameChannelIncludesPromptTokens(t *testing.T) {
	db := setupContextFallbackTestDB(t)
	settings := dto.ChannelSettings{
		ModelSystemPrompts: map[string]string{"MODEL_A": strings.Repeat("source prompt ", 80)},
		ModelContextFallbacks: map[string]dto.ModelContextFallback{
			"MODEL_A": {
				SourceContextWindowTokens:   64,
				ThresholdPercent:            90,
				FallbackModel:               "MODEL_B",
				FallbackContextWindowTokens: 4096,
				RouteMode:                   dto.ContextFallbackModeSame,
			},
		},
	}
	source := newContextFallbackChannel("source", "MODEL_A,MODEL_B", settings)
	require.NoError(t, db.Create(source).Error)
	c := setupContextFallbackGinContext(t, source)
	request := newContextFallbackRequest()
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A",
		RoutingModelName:   "MODEL_A",
		AttemptModelName:   "MODEL_A",
		OriginModelName:    "MODEL_A",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        projecttypes.RelayFormatOpenAI,
	}

	meta, apiErr := prepareContextFallback(c, info, request, 0)

	require.Nil(t, apiErr)
	require.NotNil(t, meta)
	require.True(t, info.IsContextFallbackActive())
	assert.Equal(t, "MODEL_B", info.GetAttemptModelName())
	assert.Equal(t, source.Id, info.ContextFallback.TargetChannelID)
	assert.Greater(t, info.ContextFallback.SourcePromptTokens, 0)
	assert.Greater(t, info.ContextFallback.TargetPromptTokens, 0)
	assert.Equal(t, "MODEL_A", common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
	assert.Equal(t, "MODEL_B", common.GetContextKeyString(c, constant.ContextKeyAttemptModel))
}

func TestGetChannelContextFallbackSameChannelStaysOnSource(t *testing.T) {
	db := setupContextFallbackTestDB(t)
	settings := dto.ChannelSettings{ModelContextFallbacks: map[string]dto.ModelContextFallback{
		"MODEL_A": {
			SourceContextWindowTokens:   1,
			FallbackModel:               "MODEL_B",
			FallbackContextWindowTokens: 4096,
			RouteMode:                   dto.ContextFallbackModeSame,
		},
	}}
	source := newContextFallbackChannel("source", "MODEL_A,MODEL_B", settings)
	other := newContextFallbackChannel("other", "MODEL_B", dto.ChannelSettings{})
	require.NoError(t, db.Create(source).Error)
	require.NoError(t, db.Create(other).Error)
	c := setupContextFallbackGinContext(t, source)
	request := newContextFallbackRequest()
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A",
		RoutingModelName:   "MODEL_A",
		AttemptModelName:   "MODEL_A",
		OriginModelName:    "MODEL_A",
		TokenGroup:         "default",
		UsingGroup:         "default",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        projecttypes.RelayFormatOpenAI,
		ChannelMeta:        &relaycommon.ChannelMeta{},
		Request:            request,
	}
	_, apiErr := prepareContextFallback(c, info, request, 0)
	require.Nil(t, apiErr)
	retry := 0

	selected, apiErr := getChannel(c, info, &service.RetryParam{
		Ctx:        c,
		TokenGroup: "default",
		ModelName:  "MODEL_B",
		Retry:      &retry,
	})

	require.Nil(t, apiErr)
	require.NotNil(t, selected)
	assert.Equal(t, source.Id, selected.Id)
}

func TestPrepareContextFallbackCrossChannelUsesTargetPrompt(t *testing.T) {
	db := setupContextFallbackTestDB(t)
	sourceSettings := dto.ChannelSettings{
		ModelSystemPrompts: map[string]string{"MODEL_A": strings.Repeat("source prompt ", 80)},
		ModelContextFallbacks: map[string]dto.ModelContextFallback{
			"MODEL_A": {
				SourceContextWindowTokens:   64,
				ThresholdPercent:            90,
				FallbackModel:               "MODEL_B",
				FallbackContextWindowTokens: 4096,
				RouteMode:                   dto.ContextFallbackModeCross,
			},
		},
	}
	targetSettings := dto.ChannelSettings{
		ModelSystemPrompts: map[string]string{"MODEL_B": strings.Repeat("target prompt ", 20)},
	}
	source := newContextFallbackChannel("source", "MODEL_A,MODEL_B", sourceSettings)
	target := newContextFallbackChannel("target", "MODEL_B", targetSettings)
	require.NoError(t, db.Create(source).Error)
	require.NoError(t, db.Create(target).Error)
	rule := sourceSettings.ModelContextFallbacks["MODEL_A"]
	rule.TargetChannelIDs = []int{source.Id, target.Id}
	sourceSettings.ModelContextFallbacks["MODEL_A"] = rule
	source.SetSetting(sourceSettings)
	require.NoError(t, db.Model(source).Update("setting", source.Setting).Error)

	c := setupContextFallbackGinContext(t, source)
	request := newContextFallbackRequest()
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A",
		RoutingModelName:   "MODEL_A",
		AttemptModelName:   "MODEL_A",
		OriginModelName:    "MODEL_A",
		TokenGroup:         "default",
		UsingGroup:         "default",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        projecttypes.RelayFormatOpenAI,
	}

	_, apiErr := prepareContextFallback(c, info, request, 0)

	require.Nil(t, apiErr)
	require.True(t, info.IsContextFallbackActive())
	assert.Equal(t, "MODEL_B", info.GetAttemptModelName())
	assert.Equal(t, source.Id, info.ContextFallback.SourceChannelID)
	assert.Equal(t, target.Id, info.ContextFallback.TargetChannelID)
	assert.Greater(t, info.ContextFallback.SourcePromptTokens, info.ContextFallback.TargetPromptTokens)
	assert.Greater(t, info.ContextFallback.TargetPromptTokens, 0)
	assert.Equal(t, target.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
	assert.Equal(t, "MODEL_A", common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
}

func TestPrepareContextFallbackBelowThresholdKeepsSourceModel(t *testing.T) {
	db := setupContextFallbackTestDB(t)
	settings := dto.ChannelSettings{ModelContextFallbacks: map[string]dto.ModelContextFallback{
		"MODEL_A": {
			SourceContextWindowTokens:   4096,
			FallbackModel:               "MODEL_B",
			FallbackContextWindowTokens: 8192,
			RouteMode:                   dto.ContextFallbackModeSame,
		},
	}}
	source := newContextFallbackChannel("source", "MODEL_A,MODEL_B", settings)
	require.NoError(t, db.Create(source).Error)
	c := setupContextFallbackGinContext(t, source)
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A",
		RoutingModelName:   "MODEL_A",
		AttemptModelName:   "MODEL_A",
		OriginModelName:    "MODEL_A",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        projecttypes.RelayFormatOpenAI,
	}

	_, apiErr := prepareContextFallback(c, info, newContextFallbackRequest(), 0)

	require.Nil(t, apiErr)
	assert.False(t, info.IsContextFallbackActive())
	assert.Equal(t, "MODEL_A", info.GetAttemptModelName())
	assert.Equal(t, source.Id, common.GetContextKeyInt(c, constant.ContextKeyChannelId))
}

func TestPrepareContextFallbackPassThroughRecordsBypass(t *testing.T) {
	db := setupContextFallbackTestDB(t)
	settings := dto.ChannelSettings{
		PassThroughBodyEnabled: true,
		ModelContextFallbacks: map[string]dto.ModelContextFallback{
			"MODEL_A": {
				SourceContextWindowTokens:   1,
				FallbackModel:               "MODEL_B",
				FallbackContextWindowTokens: 8192,
				RouteMode:                   dto.ContextFallbackModeSame,
			},
		},
	}
	source := newContextFallbackChannel("source", "MODEL_A,MODEL_B", settings)
	require.NoError(t, db.Create(source).Error)
	c := setupContextFallbackGinContext(t, source)
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A",
		RoutingModelName:   "MODEL_A",
		AttemptModelName:   "MODEL_A",
		OriginModelName:    "MODEL_A",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        projecttypes.RelayFormatOpenAI,
	}

	_, apiErr := prepareContextFallback(c, info, newContextFallbackRequest(), 0)

	require.Nil(t, apiErr)
	require.NotNil(t, info.ContextFallback)
	assert.False(t, info.ContextFallback.Applied)
	assert.Equal(t, "pass_through", info.ContextFallback.BypassReason)
}

func TestPrepareContextFallbackRejectsOversizedTarget(t *testing.T) {
	db := setupContextFallbackTestDB(t)
	settings := dto.ChannelSettings{
		ModelSystemPrompts: map[string]string{"MODEL_A": strings.Repeat("source prompt ", 80)},
		ModelContextFallbacks: map[string]dto.ModelContextFallback{
			"MODEL_A": {
				SourceContextWindowTokens:   64,
				FallbackModel:               "MODEL_B",
				FallbackContextWindowTokens: 1,
				RouteMode:                   dto.ContextFallbackModeSame,
			},
		},
	}
	source := newContextFallbackChannel("source", "MODEL_A,MODEL_B", settings)
	require.NoError(t, db.Create(source).Error)
	c := setupContextFallbackGinContext(t, source)
	info := &relaycommon.RelayInfo{
		RequestedModelName: "MODEL_A",
		RoutingModelName:   "MODEL_A",
		AttemptModelName:   "MODEL_A",
		OriginModelName:    "MODEL_A",
		RelayMode:          relayconstant.RelayModeChatCompletions,
		RelayFormat:        projecttypes.RelayFormatOpenAI,
	}

	_, apiErr := prepareContextFallback(c, info, newContextFallbackRequest(), 0)

	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, projecttypes.ErrorCode("context_length_exceeded"), apiErr.GetErrorCode())
}

func TestSupportsContextFallbackTextProtocols(t *testing.T) {
	chatInfo := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions}
	completionInfo := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeCompletions}
	tests := []struct {
		name    string
		info    *relaycommon.RelayInfo
		request dto.Request
		want    bool
	}{
		{name: "chat", info: chatInfo, request: &dto.GeneralOpenAIRequest{}, want: true},
		{name: "legacy completion", info: completionInfo, request: &dto.GeneralOpenAIRequest{}, want: false},
		{name: "responses", info: chatInfo, request: &dto.OpenAIResponsesRequest{}, want: true},
		{name: "responses compact", info: chatInfo, request: &dto.OpenAIResponsesCompactionRequest{}, want: true},
		{name: "claude", info: chatInfo, request: &dto.ClaudeRequest{}, want: true},
		{name: "gemini", info: chatInfo, request: &dto.GeminiChatRequest{}, want: true},
		{name: "nil request", info: chatInfo, request: nil, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, supportsContextFallback(test.info, test.request))
		})
	}
}

func TestHasProviderStateReference(t *testing.T) {
	assert.True(t, hasProviderStateReference(&dto.OpenAIResponsesRequest{PreviousResponseID: "response-id"}))
	assert.True(t, hasProviderStateReference(&dto.OpenAIResponsesCompactionRequest{PreviousResponseID: "response-id"}))
	assert.False(t, hasProviderStateReference(&dto.OpenAIResponsesRequest{}))
	assert.False(t, hasProviderStateReference(&dto.GeneralOpenAIRequest{}))
}
