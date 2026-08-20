package helper

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const maxInputModalityInspectionDepth = 32

// DetectInputModalities returns the stable set of modalities present in one parsed LLM request.
func DetectInputModalities(request dto.Request) []types.InputModality {
	modalities := []types.InputModality{types.InputModalityText}
	if requestContainsImage(request) {
		modalities = append(modalities, types.InputModalityImage)
	}
	return modalities
}

// ValidateRequestInputModalities applies the selected channel and global capability declaration.
func ValidateRequestInputModalities(c *gin.Context, requestedModel string, request dto.Request) *types.NewAPIError {
	channelSettings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	allowed, configured, _ := model_setting.ResolveModelInputModalities(requestedModel, channelSettings)
	if !configured || !containsInputModality(DetectInputModalities(request), types.InputModalityImage) {
		return nil
	}
	if containsInputModality(allowed, types.InputModalityImage) {
		return nil
	}

	err := fmt.Errorf("model %s has no declared image input capability", requestedModel)
	return types.NewOpenAIError(
		err,
		types.ErrorCodeUnsupportedInputModality,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}

func requestContainsImage(request dto.Request) bool {
	switch value := request.(type) {
	case *dto.GeneralOpenAIRequest:
		for _, message := range value.Messages {
			if contentBlocksContainType(message.Content, dto.ContentTypeImageURL) {
				return true
			}
		}
	case *dto.OpenAIResponsesRequest:
		return rawJSONContainsType(value.Input, "input_image")
	case *dto.OpenAIResponsesCompactionRequest:
		return rawJSONContainsType(value.Input, "input_image")
	case *dto.ClaudeRequest:
		if anyContainsType(value.System, "image", 0) {
			return true
		}
		for _, message := range value.Messages {
			if anyContainsType(message.Content, "image", 0) {
				return true
			}
		}
	case *dto.GeminiChatRequest:
		return geminiRequestContainsImage(value)
	}
	return false
}

func contentBlocksContainType(value any, target string) bool {
	switch content := value.(type) {
	case map[string]any:
		return content["type"] == target
	case []any:
		for _, block := range content {
			if item, ok := block.(map[string]any); ok && item["type"] == target {
				return true
			}
		}
	}
	return false
}

func containsInputModality(modalities []types.InputModality, target types.InputModality) bool {
	for _, modality := range modalities {
		if modality == target {
			return true
		}
	}
	return false
}

func anyContainsType(value any, target string, depth int) bool {
	if depth > maxInputModalityInspectionDepth {
		return false
	}
	switch item := value.(type) {
	case map[string]any:
		if itemType, ok := item["type"].(string); ok && itemType == target {
			return true
		}
		for _, nested := range item {
			if anyContainsType(nested, target, depth+1) {
				return true
			}
		}
	case []any:
		for _, nested := range item {
			if anyContainsType(nested, target, depth+1) {
				return true
			}
		}
	}
	return false
}

func rawJSONContainsType(raw []byte, target string) bool {
	if len(raw) == 0 || !gjson.ValidBytes(raw) {
		return false
	}
	return gjsonValueContainsType(gjson.ParseBytes(raw), target, 0)
}

func gjsonValueContainsType(value gjson.Result, target string, depth int) bool {
	if depth > maxInputModalityInspectionDepth || (!value.IsObject() && !value.IsArray()) {
		return false
	}
	found := false
	value.ForEach(func(key, nested gjson.Result) bool {
		if value.IsObject() && key.String() == "type" && nested.String() == target {
			found = true
			return false
		}
		if gjsonValueContainsType(nested, target, depth+1) {
			found = true
			return false
		}
		return true
	})
	return found
}

func geminiRequestContainsImage(request *dto.GeminiChatRequest) bool {
	if request == nil {
		return false
	}
	if request.SystemInstructions != nil && geminiContentContainsImage(*request.SystemInstructions) {
		return true
	}
	for _, content := range request.Contents {
		if geminiContentContainsImage(content) {
			return true
		}
	}
	for index := range request.Requests {
		if geminiRequestContainsImage(&request.Requests[index]) {
			return true
		}
	}
	return false
}

func geminiContentContainsImage(content dto.GeminiChatContent) bool {
	for _, part := range content.Parts {
		if part.InlineData != nil && strings.HasPrefix(strings.ToLower(part.InlineData.MimeType), "image/") {
			return true
		}
		if part.FileData != nil && strings.HasPrefix(strings.ToLower(part.FileData.MimeType), "image/") {
			return true
		}
	}
	return false
}
