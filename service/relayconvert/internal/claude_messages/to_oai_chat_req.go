package claudemessages

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relaymeta "github.com/QuantumNous/new-api/service/relayconvert/internal/meta"
	"github.com/QuantumNous/new-api/service/relaynormalize"
	"github.com/QuantumNous/new-api/types"
)

const (
	webSearchMaxUsesLow    = 1
	webSearchMaxUsesMedium = 5
	webSearchMaxUsesHigh   = 10
)

type openRouterRequestReasoning struct {
	Enabled   bool   `json:"enabled"`
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	Exclude   bool   `json:"exclude,omitempty"`
}

func ClaudeMessagesRequestToOpenAIChat(claudeRequest dto.ClaudeRequest, info *relaycommon.RelayInfo) (*dto.GeneralOpenAIRequest, error) {
	openAIRequest := dto.GeneralOpenAIRequest{
		Model:       claudeRequest.Model,
		Temperature: claudeRequest.Temperature,
	}
	if claudeRequest.MaxTokens != nil {
		openAIRequest.MaxTokens = common.GetPointer(*claudeRequest.MaxTokens)
	}
	if claudeRequest.TopP != nil {
		openAIRequest.TopP = common.GetPointer(*claudeRequest.TopP)
	}
	if claudeRequest.TopK != nil {
		openAIRequest.TopK = common.GetPointer(*claudeRequest.TopK)
	}
	if claudeRequest.Stream != nil {
		openAIRequest.Stream = common.GetPointer(*claudeRequest.Stream)
	}

	isOpenRouter := relaymeta.RelayInfoChannelType(info) == constant.ChannelTypeOpenRouter
	if isOpenRouter {
		if effort := claudeRequest.GetEfforts(); effort != "" {
			effortBytes, _ := common.Marshal(effort)
			openAIRequest.Verbosity = effortBytes
		}
		if claudeRequest.Thinking != nil {
			var reasoningConfig openRouterRequestReasoning
			if claudeRequest.Thinking.Type == "enabled" {
				reasoningConfig = openRouterRequestReasoning{
					Enabled:   true,
					MaxTokens: claudeRequest.Thinking.GetBudgetTokens(),
				}
			} else if claudeRequest.Thinking.Type == "adaptive" {
				reasoningConfig = openRouterRequestReasoning{
					Enabled: true,
				}
			}
			reasoningJSON, err := common.Marshal(reasoningConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal reasoning: %w", err)
			}
			openAIRequest.Reasoning = reasoningJSON
		}
	} else if info != nil {
		thinkingSuffix := "-thinking"
		if strings.HasSuffix(info.OriginModelName, thinkingSuffix) &&
			!strings.HasSuffix(openAIRequest.Model, thinkingSuffix) {
			openAIRequest.Model = openAIRequest.Model + thinkingSuffix
		}
	}

	if len(claudeRequest.StopSequences) == 1 {
		openAIRequest.Stop = claudeRequest.StopSequences[0]
	} else if len(claudeRequest.StopSequences) > 1 {
		openAIRequest.Stop = claudeRequest.StopSequences
	}

	tools, _ := common.Any2Type[[]dto.Tool](claudeRequest.Tools)
	openAITools := make([]dto.ToolCallRequest, 0)
	for _, claudeTool := range tools {
		openAITool := dto.ToolCallRequest{
			Type: "function",
			Function: dto.FunctionRequest{
				Name:        claudeTool.Name,
				Description: claudeTool.Description,
				Parameters:  claudeTool.InputSchema,
			},
		}
		openAITools = append(openAITools, openAITool)
	}
	openAIRequest.Tools = openAITools

	openAIMessages := make([]dto.Message, 0)
	if claudeRequest.System != nil {
		if claudeRequest.IsStringSystem() && claudeRequest.GetStringSystem() != "" {
			openAIMessage := dto.Message{
				Role: "system",
			}
			openAIMessage.SetStringContent(claudeRequest.GetStringSystem())
			openAIMessages = append(openAIMessages, openAIMessage)
		} else {
			systems := claudeRequest.ParseSystem()
			if len(systems) > 0 {
				openAIMessage := dto.Message{
					Role: "system",
				}
				isOpenRouterClaude := isOpenRouter && strings.HasPrefix(relaymeta.RelayInfoUpstreamModelName(info), "anthropic/claude")
				if isOpenRouterClaude {
					systemMediaMessages := make([]dto.MediaContent, 0, len(systems))
					for _, system := range systems {
						message := dto.MediaContent{
							Type:         "text",
							Text:         system.GetText(),
							CacheControl: system.CacheControl,
						}
						systemMediaMessages = append(systemMediaMessages, message)
					}
					openAIMessage.SetMediaContent(systemMediaMessages)
				} else {
					systemStr := ""
					for _, system := range systems {
						if system.Text != nil {
							systemStr += *system.Text
						}
					}
					openAIMessage.SetStringContent(systemStr)
				}
				openAIMessages = append(openAIMessages, openAIMessage)
			}
		}
	}

	toolIDNormalizer := relaynormalize.NewClaudeToolIDNormalizer()
	for _, claudeMessage := range claudeRequest.Messages {
		if claudeMessage.IsStringContent() {
			continue
		}
		content, err := claudeMessage.ParseContent()
		if err != nil {
			return nil, err
		}
		for _, mediaMsg := range content {
			if mediaMsg.Type == "tool_use" {
				toolIDNormalizer.Normalize(mediaMsg.Id)
			}
		}
	}

	for _, claudeMessage := range claudeRequest.Messages {
		openAIMessage := dto.Message{
			Role: claudeMessage.Role,
		}
		if claudeMessage.IsStringContent() {
			content := claudeMessage.GetStringContent()
			if claudeMessage.Role == "assistant" && strings.TrimSpace(content) == "" {
				continue
			}
			openAIMessage.SetStringContent(content)
		} else {
			content, err := claudeMessage.ParseContent()
			if err != nil {
				return nil, err
			}
			var toolCalls []dto.ToolCallRequest
			mediaMessages := make([]dto.MediaContent, 0, len(content))
			var reasoningContent strings.Builder
			opaqueBlocksSkipped := 0

			for _, mediaMsg := range content {
				switch mediaMsg.Type {
				case "thinking":
					// Chat wire can carry readable reasoning text, but not Claude's opaque signature.
					if claudeMessage.Role == "assistant" && mediaMsg.Thinking != nil {
						reasoningContent.WriteString(*mediaMsg.Thinking)
					}
				case "redacted_thinking":
					// Encrypted payloads stay opaque instead of being mislabeled as readable reasoning.
					if claudeMessage.Role == "assistant" {
						opaqueBlocksSkipped++
					}
				case "text", "input_text":
					text := mediaMsg.GetText()
					if claudeMessage.Role == "assistant" && strings.TrimSpace(text) == "" {
						continue
					}
					message := dto.MediaContent{
						Type:         "text",
						Text:         text,
						CacheControl: mediaMsg.CacheControl,
					}
					mediaMessages = append(mediaMessages, message)
				case "image":
					imageData := fmt.Sprintf("data:%s;base64,%s", mediaMsg.Source.MediaType, mediaMsg.Source.Data)
					mediaMessage := dto.MediaContent{
						Type:     "image_url",
						ImageUrl: &dto.MessageImageUrl{Url: imageData},
					}
					mediaMessages = append(mediaMessages, mediaMessage)
				case "tool_use":
					normalizedID, _, _ := toolIDNormalizer.Normalize(mediaMsg.Id)
					toolCall := dto.ToolCallRequest{
						ID:   normalizedID,
						Type: "function",
						Function: dto.FunctionRequest{
							Name:      mediaMsg.Name,
							Arguments: requestToJSONString(mediaMsg.Input),
						},
					}
					toolCalls = append(toolCalls, toolCall)
				case "tool_result":
					normalizedID, _, _ := toolIDNormalizer.Normalize(mediaMsg.ToolUseId)
					toolName := mediaMsg.Name
					if toolName == "" {
						toolName = claudeRequest.SearchToolNameByToolCallId(mediaMsg.ToolUseId)
					}
					oaiToolMessage := dto.Message{
						Role:       "tool",
						Name:       &toolName,
						ToolCallId: normalizedID,
					}
					if mediaMsg.IsStringContent() {
						oaiToolMessage.SetStringContent(mediaMsg.GetStringContent())
					} else {
						mediaContents := mediaMsg.ParseMediaContent()
						encodedJSON, _ := common.Marshal(mediaContents)
						oaiToolMessage.SetStringContent(string(encodedJSON))
					}
					openAIMessages = append(openAIMessages, oaiToolMessage)
				}
			}

			if len(toolCalls) > 0 {
				openAIMessage.SetToolCalls(toolCalls)
			}
			if len(mediaMessages) > 0 {
				openAIMessage.SetMediaContent(mediaMessages)
			}
			hasVisibleAssistantPayload := len(mediaMessages) > 0 || len(toolCalls) > 0
			if reasoningContent.Len() > 0 && hasVisibleAssistantPayload {
				reasoning := reasoningContent.String()
				openAIMessage.ReasoningContent = &reasoning
				info.AddReasoningHistoryAudit(
					types.RelayFormatClaude,
					types.RelayFormatOpenAI,
					relaycommon.ReasoningHistoryReasonPreserved,
					1, 0, 0, 0,
				)
			}
			if opaqueBlocksSkipped > 0 {
				info.AddReasoningHistoryAudit(
					types.RelayFormatClaude,
					types.RelayFormatOpenAI,
					relaycommon.ReasoningHistoryReasonOpaqueBlockSkipped,
					0, 0, opaqueBlocksSkipped, 0,
				)
			}
			if claudeMessage.Role == "assistant" && !hasVisibleAssistantPayload &&
				(reasoningContent.Len() > 0 || opaqueBlocksSkipped > 0) {
				info.AddDroppedReasoningOnlyMessages(types.RelayFormatClaude, types.RelayFormatOpenAI, 1)
			}
		}
		if openAIMessage.Content != nil || len(openAIMessage.ParseToolCalls()) > 0 {
			openAIMessages = append(openAIMessages, openAIMessage)
		}
	}

	openAIRequest.Messages = openAIMessages
	if err := validateOpenAIAssistantMessages(openAIRequest.Messages); err != nil {
		return nil, err
	}
	return &openAIRequest, nil
}

// validateOpenAIAssistantMessages prevents invalid assistant history from reaching the OpenAI wire format.
func validateOpenAIAssistantMessages(messages []dto.Message) error {
	for index, message := range messages {
		if message.Role != "assistant" {
			continue
		}
		functionCall := bytes.TrimSpace(message.FunctionCall)
		hasFunctionCall := len(functionCall) > 0 && !bytes.Equal(functionCall, []byte("null"))
		hasContent := false
		if message.Content != nil {
			if message.IsStringContent() {
				hasContent = strings.TrimSpace(message.StringContent()) != ""
			} else {
				for _, content := range message.ParseContent() {
					if content.Type != "text" && content.Type != "input_text" {
						hasContent = true
						break
					}
					if strings.TrimSpace(content.Text) != "" {
						hasContent = true
						break
					}
				}
			}
		}
		if !hasContent && len(message.ParseToolCalls()) == 0 && !hasFunctionCall {
			return fmt.Errorf("assistant message %d must have content, tool_calls, or function_call", index)
		}
	}
	return nil
}

func requestToJSONString(v interface{}) string {
	b, err := common.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
