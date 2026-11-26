package controller

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/samber/lo"
	_ "golang.org/x/image/webp"
)

const maxChannelTestImageBytes = 4 * 1024 * 1024
const maxChannelTestBodyBytes = 6 * 1024 * 1024

type channelTestImage struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

func validateChannelTestImages(images []channelTestImage) error {
	if len(images) > 1 {
		return errors.New("Channel tests accept at most one image.")
	}
	for _, input := range images {
		if input.MimeType != "image/png" && input.MimeType != "image/jpeg" && input.MimeType != "image/webp" {
			return errors.New("Use a PNG, JPEG or WebP image.")
		}
		if len(input.Data) > base64.StdEncoding.EncodedLen(maxChannelTestImageBytes) {
			return errors.New("Test images must not exceed 4 MiB.")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(input.Data)
		if err != nil || len(data) == 0 {
			return errors.New("Invalid test image data.")
		}
		if len(data) > maxChannelTestImageBytes {
			return errors.New("Test images must not exceed 4 MiB.")
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || input.MimeType != "image/"+format {
			return errors.New("Invalid test image data.")
		}
		// Bound decoded allocation before checking the complete image.
		if config.Width <= 0 || config.Height <= 0 || config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > 16000000 {
			return errors.New("Test images must fit within 4096 pixels and 16 megapixels.")
		}
		if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
			return errors.New("Invalid test image data.")
		}
	}
	return nil
}

func buildTestRequest(modelName, endpointType string, channel *model.Channel, rawOptions any) dto.Request {
	request := buildTextTestRequest(modelName, endpointType, channel, rawOptions)
	options, _ := rawOptions.(channelTestOptions)
	if len(options.images) == 0 {
		return request
	}
	input := options.images[0]
	dataURL := "data:" + input.MimeType + ";base64," + input.Data
	switch req := request.(type) {
	case *dto.GeneralOpenAIRequest:
		req.Messages[0].Content = []dto.MediaContent{
			{Type: dto.ContentTypeText, Text: options.userPrompt},
			{Type: dto.ContentTypeImageURL, ImageUrl: &dto.MessageImageUrl{Url: dataURL}},
		}
	case *dto.OpenAIResponsesRequest:
		req.Input = []byte(common.GetJsonString([]map[string]any{{"role": "user", "content": []map[string]string{
			{"type": "input_text", "text": options.userPrompt},
			{"type": "input_image", "image_url": dataURL},
		}}}))
	case *dto.ClaudeRequest:
		req.Messages[0].Content = []dto.ClaudeMediaMessage{
			{Type: "image", Source: &dto.ClaudeMessageSource{Type: "base64", MediaType: input.MimeType, Data: input.Data}},
			{Type: "text", Text: lo.ToPtr(options.userPrompt)},
		}
	case *dto.GeminiChatRequest:
		req.Contents[0].Parts = append(req.Contents[0].Parts, dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: input.MimeType, Data: input.Data}})
	}
	return request
}
