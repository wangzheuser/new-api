package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
)

// contextImageTokenMeta supplements image locations not collected by the existing request counter.
func contextImageTokenMeta(request dto.Request) (*types.TokenCountMeta, error) {
	meta := request.GetTokenCountMeta()
	switch request := request.(type) {
	case *dto.ClaudeRequest:
		for _, message := range request.Messages {
			if message.IsStringContent() {
				continue
			}
			blocks, err := message.ParseContent()
			if err != nil {
				return nil, err
			}
			for _, block := range blocks {
				if block.Type != "tool_result" || block.Content == nil {
					continue
				}
				if _, text := block.Content.(string); text {
					continue
				}
				encoded, err := common.Marshal(block.Content)
				if err != nil {
					return nil, err
				}
				var nested []dto.ClaudeMediaMessage
				if err := common.Unmarshal(encoded, &nested); err != nil {
					return nil, err
				}
				for _, item := range nested {
					if item.Type == "image" {
						meta.Files = append(meta.Files, &types.FileMeta{FileType: types.FileTypeImage, Source: item.ToFileSource()})
					}
				}
			}
		}
	case *dto.GeminiChatRequest:
		contents := request.Contents
		if request.SystemInstructions != nil {
			contents = append([]dto.GeminiChatContent{*request.SystemInstructions}, contents...)
		}
		for _, content := range contents {
			if err := appendGeminiContextImages(meta, content.Parts, false); err != nil {
				return nil, err
			}
		}
	}
	return meta, nil
}

// appendGeminiContextImages includes file references and function-result images without double-counting top-level inline data.
func appendGeminiContextImages(meta *types.TokenCountMeta, parts []dto.GeminiPart, nested bool) error {
	for _, part := range parts {
		if nested && part.InlineData != nil {
			meta.Files = append(meta.Files, &types.FileMeta{FileType: types.FileTypeImage, Source: part.InlineData.ToFileSource()})
		}
		if part.FileData != nil {
			meta.Files = append(meta.Files, &types.FileMeta{FileType: types.FileTypeImage, Source: types.NewFileSourceFromData(part.FileData.FileUri, part.FileData.MimeType)})
		}
		if part.FunctionResponse != nil && len(part.FunctionResponse.Parts) > 0 {
			var resultParts []dto.GeminiPart
			if err := common.Unmarshal(part.FunctionResponse.Parts, &resultParts); err != nil {
				return err
			}
			if err := appendGeminiContextImages(meta, resultParts, true); err != nil {
				return err
			}
		}
	}
	return nil
}
