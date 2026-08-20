package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRelayRejectsImageBeforeProtocolSpecificUpstreamFlow verifies early rejection and protocol envelopes.
func TestRelayRejectsImageBeforeProtocolSpecificUpstreamFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name           string
		path           string
		format         types.RelayFormat
		body           string
		assertEnvelope func(t *testing.T, body []byte)
	}{
		{
			name:   "chat completions",
			path:   "/v1/chat/completions",
			format: types.RelayFormatOpenAI,
			body:   `{"model":"source","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a.png"}}]}]}`,
			assertEnvelope: func(t *testing.T, body []byte) {
				var response struct {
					Error types.OpenAIError `json:"error"`
				}
				require.NoError(t, common.Unmarshal(body, &response))
				assert.Equal(t, string(types.ErrorCodeUnsupportedInputModality), response.Error.Code)
			},
		},
		{
			name:   "responses",
			path:   "/v1/responses",
			format: types.RelayFormatOpenAIResponses,
			body:   `{"model":"source","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/a.png"}]}]}`,
			assertEnvelope: func(t *testing.T, body []byte) {
				var response struct {
					Error types.OpenAIError `json:"error"`
				}
				require.NoError(t, common.Unmarshal(body, &response))
				assert.Equal(t, string(types.ErrorCodeUnsupportedInputModality), response.Error.Code)
			},
		},
		{
			name:   "responses compaction",
			path:   "/v1/responses/compact",
			format: types.RelayFormatOpenAIResponsesCompaction,
			body:   `{"model":"source","input":[{"type":"input_image","image_url":"https://example.test/a.png"}]}`,
			assertEnvelope: func(t *testing.T, body []byte) {
				var response struct {
					Error types.OpenAIError `json:"error"`
				}
				require.NoError(t, common.Unmarshal(body, &response))
				assert.Equal(t, string(types.ErrorCodeUnsupportedInputModality), response.Error.Code)
			},
		},
		{
			name:   "messages",
			path:   "/v1/messages",
			format: types.RelayFormatClaude,
			body:   `{"model":"source","max_tokens":16,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`,
			assertEnvelope: func(t *testing.T, body []byte) {
				var response struct {
					Type  string            `json:"type"`
					Error types.ClaudeError `json:"error"`
				}
				require.NoError(t, common.Unmarshal(body, &response))
				assert.Equal(t, "error", response.Type)
				assert.Equal(t, string(types.ErrorCodeUnsupportedInputModality), response.Error.Type)
			},
		},
		{
			name:   "generate content",
			path:   "/v1beta/models/source:generateContent",
			format: types.RelayFormatGemini,
			body:   `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`,
			assertEnvelope: func(t *testing.T, body []byte) {
				var response dto.GeminiErrorResponse
				require.NoError(t, common.Unmarshal(body, &response))
				assert.Equal(t, http.StatusBadRequest, response.Error.Code)
				assert.Equal(t, "INVALID_ARGUMENT", response.Error.Status)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "source")
			common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{
				ModelInputModalities: types.ModelInputModalities{
					"source": {types.InputModalityText},
				},
			})

			Relay(ctx, test.format)

			assert.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "model source has no declared image input capability")
			test.assertEnvelope(t, recorder.Body.Bytes())
		})
	}
}

// TestResolveConfiguredFinalRelayErrorPreservesInputModalityError verifies retry gates stay client-visible.
func TestResolveConfiguredFinalRelayErrorPreservesInputModalityError(t *testing.T) {
	modalityErr := types.NewOpenAIError(
		errors.New("model source has no declared image input capability"),
		types.ErrorCodeUnsupportedInputModality,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
	info := &relaycommon.RelayInfo{LastError: modalityErr}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	assert.Same(t, modalityErr, resolveConfiguredFinalRelayError(ctx, info))
}

// TestGetChannelRejectsRetryChannelByRequestedModel verifies retry setup reads the new channel declaration.
func TestGetChannelRejectsRetryChannelByRequestedModel(t *testing.T) {
	db := setupPerformanceOptionTest(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })

	priority := int64(1)
	weight := uint(100)
	mapping := `{"source":"upstream"}`
	channel := &model.Channel{
		Type:         constant.ChannelTypeOpenAI,
		Key:          "TOKEN",
		Status:       common.ChannelStatusEnabled,
		Name:         "retry text-only",
		Models:       "source",
		Group:        "default",
		Priority:     &priority,
		Weight:       &weight,
		ModelMapping: &mapping,
	}
	channel.SetSetting(dto.ChannelSettings{ModelInputModalities: types.ModelInputModalities{
		"source":   {types.InputModalityText},
		"upstream": {types.InputModalityText, types.InputModalityImage},
	}})
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     "default",
		Model:     "source",
		ChannelId: channel.Id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyOriginalModel, "source")
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{
		Role: "user",
		Content: []any{map[string]any{
			"type": "image_url", "image_url": map[string]any{"url": "https://example.test/a.png"},
		}},
	}}}
	info := &relaycommon.RelayInfo{
		Request:            request,
		RequestedModelName: "source",
		RoutingModelName:   "source",
		AttemptModelName:   "source",
		RelayFormat:        types.RelayFormatOpenAI,
		ChannelMeta:        &relaycommon.ChannelMeta{},
	}
	retry := &service.RetryParam{
		Ctx:         ctx,
		TokenGroup:  "default",
		ModelName:   "source",
		RequestPath: ctx.Request.URL.Path,
		RelayFormat: types.RelayFormatOpenAI,
		Retry:       common.GetPointer(0),
	}

	selected, err := getChannel(ctx, info, retry)

	assert.Nil(t, selected)
	require.NotNil(t, err)
	assert.Equal(t, types.ErrorCodeUnsupportedInputModality, err.GetErrorCode())
	assert.Equal(t, channel.Id, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
}
