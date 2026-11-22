package helper

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	rootdto "github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const maxInputModalityInspectionDepth = 32

// DetectInputModalities returns the stable set of modalities present in one parsed LLM request.
func DetectInputModalities(request any) []types.InputModality {
	modalities := []types.InputModality{types.InputModalityText}
	if requestContainsImage(request) {
		modalities = append(modalities, types.InputModalityImage)
	}
	return modalities
}

const inputModalityRequestKey = "validated_input_modality_request"

type inputModalityRequest struct {
	format   types.RelayFormat
	request  kitdto.Request
	err      error
	hasImage bool
}

// PrepareInputModalityRouting detects images and leaves validation errors to the relay handler.
func PrepareInputModalityRouting(c *gin.Context, format types.RelayFormat, modelName string) {
	request, err := GetAndValidateRequest(c, format)
	hasImage := err == nil && requestContainsImage(request)
	c.Set(inputModalityRequestKey, inputModalityRequest{format: format, request: request, err: err, hasImage: hasImage})
	if hasImage {
		if compact, ok := request.(*kitdto.OpenAIResponsesCompactionRequest); ok {
			modelName = compact.Model
		}
		service.GetChannelConstraints(c).ImageInputModel = modelName
	}
}

// HasImageInput reuses the detection result when the request was prepared before selection.
func HasImageInput(c *gin.Context, request kitdto.Request) bool {
	if value, ok := c.Get(inputModalityRequestKey); ok {
		if prepared := value.(inputModalityRequest); prepared.request == request {
			return prepared.hasImage
		}
	}
	return requestContainsImage(request)
}

// ValidateRequestInputModalities applies the selected channel and global capability declaration.
func ValidateRequestInputModalities(c *gin.Context, requestedModel string, request any) *types.NewAPIError {
	channelSettings, _ := common.GetContextKeyType[rootdto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	if model_setting.AllowsImageInput(requestedModel, channelSettings) {
		return nil
	}
	hasImage := false
	if parsed, ok := request.(kitdto.Request); ok {
		hasImage = HasImageInput(c, parsed)
	} else {
		hasImage = requestContainsImage(request)
	}
	if hasImage {
		return types.UnsupportedImageInputError(requestedModel)
	}
	return nil
}

func requestContainsImage(request any) bool {
	switch value := request.(type) {
	case *rootdto.GeneralOpenAIRequest:
		for _, message := range value.Messages {
			if contentBlocksContainType(message.Content, rootdto.ContentTypeImageURL) {
				return true
			}
		}
	case *rootdto.OpenAIResponsesRequest:
		return rawJSONContainsType(value.Input, "input_image")
	case *rootdto.OpenAIResponsesCompactionRequest:
		return rawJSONContainsType(value.Input, "input_image")
	case *rootdto.ClaudeRequest:
		if anyContainsType(value.System, "image", 0) {
			return true
		}
		for _, message := range value.Messages {
			if anyContainsType(message.Content, "image", 0) {
				return true
			}
		}
	case *rootdto.GeminiChatRequest:
		return geminiRequestContainsImage(value)
	case *kitdto.GeneralOpenAIRequest:
		for _, message := range value.Messages {
			if contentBlocksContainType(message.Content, kitdto.ContentTypeImageURL) {
				return true
			}
		}
	case *kitdto.OpenAIResponsesRequest:
		return rawJSONContainsType(value.Input, "input_image")
	case *kitdto.OpenAIResponsesCompactionRequest:
		return rawJSONContainsType(value.Input, "input_image")
	case *kitdto.ClaudeRequest:
		if anyContainsType(value.System, "image", 0) {
			return true
		}
		for _, message := range value.Messages {
			if anyContainsType(message.Content, "image", 0) {
				return true
			}
		}
	case *kitdto.GeminiChatRequest:
		return geminiRequestContainsImageKit(value)
	}
	return false
}

func geminiRequestContainsImageKit(request *kitdto.GeminiChatRequest) bool {
	if request.SystemInstructions != nil && geminiKitContentContainsImage(*request.SystemInstructions) {
		return true
	}
	for _, content := range request.Contents {
		if geminiKitContentContainsImage(content) {
			return true
		}
	}
	for index := range request.Requests {
		if geminiRequestContainsImageKit(&request.Requests[index]) {
			return true
		}
	}
	return false
}

func geminiKitContentContainsImage(content kitdto.GeminiChatContent) bool {
	for _, part := range content.Parts {
		if (part.InlineData != nil && isImageMIME(part.InlineData.MimeType)) ||
			(part.FileData != nil && isImageMIME(part.FileData.MimeType)) {
			return true
		}
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

func isImageMIME(mimeType string) bool {
	return strings.HasPrefix(strings.ToLower(mimeType), "image/")
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

func geminiRequestContainsImage(request *rootdto.GeminiChatRequest) bool {
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

func geminiContentContainsImage(content rootdto.GeminiChatContent) bool {
	for _, part := range content.Parts {
		if part.InlineData != nil && isImageMIME(part.InlineData.MimeType) {
			return true
		}
		if part.FileData != nil && isImageMIME(part.FileData.MimeType) {
			return true
		}
	}
	return false
}
