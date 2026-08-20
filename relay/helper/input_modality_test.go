package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectInputModalities(t *testing.T) {
	tests := []struct {
		name    string
		request dto.Request
		want    []types.InputModality
	}{
		{
			name: "chat text",
			request: &dto.GeneralOpenAIRequest{Messages: []dto.Message{{
				Role: "user", Content: "hello",
			}}},
			want: []types.InputModality{types.InputModalityText},
		},
		{
			name: "chat image",
			request: &dto.GeneralOpenAIRequest{Messages: []dto.Message{{
				Role: "user",
				Content: []any{
					map[string]any{"type": "text", "text": "describe"},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AA=="}},
				},
			}}},
			want: []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name: "chat tool result remains text baseline",
			request: &dto.GeneralOpenAIRequest{Messages: []dto.Message{{
				Role: "tool",
				Content: []any{map[string]any{
					"type": "tool_result",
					"content": []any{map[string]any{
						"type": "image_url", "image_url": "https://example.test/a.png",
					}},
				}},
			}}},
			want: []types.InputModality{types.InputModalityText},
		},
		{
			name:    "responses image",
			request: &dto.OpenAIResponsesRequest{Input: []byte(`[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/a.png"}]}]`)},
			want:    []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name:    "responses string input",
			request: &dto.OpenAIResponsesRequest{Input: []byte(`"hello"`)},
			want:    []types.InputModality{types.InputModalityText},
		},
		{
			name:    "responses URL without image type remains text baseline",
			request: &dto.OpenAIResponsesRequest{Input: []byte(`[{"image_url":{"url":"https://example.test/a.png"}}]`)},
			want:    []types.InputModality{types.InputModalityText},
		},
		{
			name:    "responses compaction image",
			request: &dto.OpenAIResponsesCompactionRequest{Input: []byte(`[{"type":"input_image","image_url":"https://example.test/a.png"}]`)},
			want:    []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name: "claude system image",
			request: &dto.ClaudeRequest{System: []any{
				map[string]any{"type": "image", "source": map[string]any{"type": "base64"}},
			}},
			want: []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name: "claude tool result image",
			request: &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{{
				Role: "user",
				Content: []any{map[string]any{
					"type":    "tool_result",
					"content": []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64"}}},
				}},
			}}},
			want: []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name: "claude message image",
			request: &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{{
				Role: "user",
				Content: []any{map[string]any{
					"type": "image", "source": map[string]any{"type": "url", "url": "https://example.test/a.png"},
				}},
			}}},
			want: []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name: "gemini inline image",
			request: &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Parts: []dto.GeminiPart{{
				InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "AA=="},
			}}}}},
			want: []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name: "gemini batch file image",
			request: &dto.GeminiChatRequest{Requests: []dto.GeminiChatRequest{{Contents: []dto.GeminiChatContent{{Parts: []dto.GeminiPart{{
				FileData: &dto.GeminiFileData{MimeType: "image/jpeg", FileUri: "gs://bucket/a.jpg"},
			}}}}}}},
			want: []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name: "gemini system instruction image",
			request: &dto.GeminiChatRequest{SystemInstructions: &dto.GeminiChatContent{Parts: []dto.GeminiPart{{
				InlineData: &dto.GeminiInlineData{MimeType: "image/webp", Data: "AA=="},
			}}}},
			want: []types.InputModality{types.InputModalityText, types.InputModalityImage},
		},
		{
			name: "gemini audio remains text baseline",
			request: &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Parts: []dto.GeminiPart{{
				InlineData: &dto.GeminiInlineData{MimeType: "audio/wav", Data: "AA=="},
			}}}}},
			want: []types.InputModality{types.InputModalityText},
		},
		{
			name: "gemini video remains text baseline",
			request: &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Parts: []dto.GeminiPart{{
				FileData: &dto.GeminiFileData{MimeType: "video/mp4", FileUri: "gs://bucket/a.mp4"},
			}}}}},
			want: []types.InputModality{types.InputModalityText},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, DetectInputModalities(test.request))
		})
	}
}

func TestValidateRequestInputModalitiesUsesRequestedModelPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	global := model_setting.GetGlobalSettings()
	previous := global.ModelInputModalities
	global.ModelInputModalities = types.ModelInputModalities{
		"source":   {types.InputModalityText},
		"upstream": {types.InputModalityText, types.InputModalityImage},
	}
	t.Cleanup(func() { global.ModelInputModalities = previous })

	request := &dto.GeneralOpenAIRequest{Messages: []dto.Message{{
		Role: "user",
		Content: []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/a.png"}},
		},
	}}}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	err := ValidateRequestInputModalities(ctx, "source", request)
	require.NotNil(t, err)
	assert.Equal(t, types.ErrorCodeUnsupportedInputModality, err.GetErrorCode())
	assert.Equal(t, 400, err.StatusCode)
	assert.True(t, types.IsSkipRetryError(err))
	assert.Equal(t, "unsupported_input_modality", err.ToClaudeError().Type)
	assert.Equal(t, "", err.ToOpenAIError().Param)

	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{
		PassThroughBodyEnabled: true,
		ModelInputModalities: types.ModelInputModalities{
			"source": {types.InputModalityText},
		},
	})
	assert.NotNil(t, ValidateRequestInputModalities(ctx, "source", request))

	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{
		PassThroughBodyEnabled: true,
		ModelInputModalities: types.ModelInputModalities{
			"source": {types.InputModalityText, types.InputModalityImage},
		},
	})
	assert.Nil(t, ValidateRequestInputModalities(ctx, "source", request))
	assert.Nil(t, ValidateRequestInputModalities(ctx, "unconfigured", request))
}
