package dto

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
)

const (
	// MaxChannelSettingBytes 保持 channels.setting 与 MySQL TEXT 上限兼容。
	MaxChannelSettingBytes      = 64*1024 - 1
	MaxModelSystemPromptEntries = 256
	MaxModelSystemPromptBytes   = 64 * 1024
	MaxModelContextFallbacks    = 256
	MaxModelProtocolOverrides   = 256
	DefaultContextThreshold     = 90
	ContextFallbackModeSame     = "same_channel"
	ContextFallbackModeCross    = "cross_channel"
)

type ProtocolConversionQuality string

type ProtocolHandlingMode string

const (
	ProtocolConversionQualityGood  ProtocolConversionQuality = "good"
	ProtocolConversionQualityFair  ProtocolConversionQuality = "fair"
	ProtocolHandlingModeNative     ProtocolHandlingMode      = "native"
	ProtocolHandlingModeNormalized ProtocolHandlingMode      = "normalized"
)

type ProtocolCapability struct {
	NonStream bool                 `json:"non_stream"`
	Stream    bool                 `json:"stream"`
	Mode      ProtocolHandlingMode `json:"mode,omitempty"`
}

type ModelProtocolProfile struct {
	Native map[constant.EndpointType]ProtocolCapability `json:"native"`
}

type ChannelProtocolPolicy struct {
	Native         map[constant.EndpointType]ProtocolCapability `json:"native,omitempty"`
	ModelOverrides map[string]ModelProtocolProfile              `json:"model_overrides,omitempty"`
	AutoConvert    bool                                         `json:"auto_convert"`
	MaxQuality     ProtocolConversionQuality                    `json:"max_quality"`
}

// ModelContextFallback 定义某个源路由模型的上下文阈值与单次兜底目标。
type ModelContextFallback struct {
	SourceContextWindowTokens   int64  `json:"source_context_window_tokens"`
	ThresholdPercent            int    `json:"threshold_percent,omitempty"`
	FallbackModel               string `json:"fallback_model"`
	FallbackContextWindowTokens int64  `json:"fallback_context_window_tokens"`
	RouteMode                   string `json:"route_mode"`
	TargetChannelIDs            []int  `json:"target_channel_ids,omitempty"`
}

// EffectiveThresholdPercent 返回规则的有效阈值百分比。
func (r ModelContextFallback) EffectiveThresholdPercent() int {
	if r.ThresholdPercent == 0 {
		return DefaultContextThreshold
	}
	return r.ThresholdPercent
}

// ThresholdTokens 使用整数运算计算源模型的触发阈值。
func (r ModelContextFallback) ThresholdTokens() int64 {
	percent := int64(r.EffectiveThresholdPercent())
	return r.SourceContextWindowTokens/100*percent + r.SourceContextWindowTokens%100*percent/100
}

type ChannelSettings struct {
	ForceFormat            bool                            `json:"force_format,omitempty"`
	ThinkingToContent      bool                            `json:"thinking_to_content,omitempty"`
	Proxy                  string                          `json:"proxy"`
	PassThroughBodyEnabled bool                            `json:"pass_through_body_enabled,omitempty"`
	SystemPrompt           string                          `json:"system_prompt,omitempty"`
	SystemPromptOverride   bool                            `json:"system_prompt_override,omitempty"`
	ModelSystemPrompts     map[string]string               `json:"model_system_prompts,omitempty"`
	ModelInputModalities   types.ModelInputModalities      `json:"model_input_modalities,omitempty"`
	ModelContextFallbacks  map[string]ModelContextFallback `json:"model_context_fallbacks,omitempty"`
	ProtocolPolicy         *ChannelProtocolPolicy          `json:"protocol_policy,omitempty"`
}

// EffectiveMaxQuality returns the configured conversion ceiling.
func (p ChannelProtocolPolicy) EffectiveMaxQuality() ProtocolConversionQuality {
	if p.MaxQuality == "" {
		return ProtocolConversionQualityFair
	}
	return p.MaxQuality
}

// NativeForModel resolves an exact model override before the channel defaults.
func (p ChannelProtocolPolicy) NativeForModel(model string) (map[constant.EndpointType]ProtocolCapability, string) {
	if override, ok := p.ModelOverrides[model]; ok {
		return override.Native, "model_override"
	}
	return p.Native, "channel_default"
}

// HasNormalizedCapability reports whether any channel or model capability requires wire normalization.
func (p ChannelProtocolPolicy) HasNormalizedCapability() bool {
	for _, capability := range p.Native {
		if capability.EffectiveMode() == ProtocolHandlingModeNormalized {
			return true
		}
	}
	for _, profile := range p.ModelOverrides {
		for _, capability := range profile.Native {
			if capability.EffectiveMode() == ProtocolHandlingModeNormalized {
				return true
			}
		}
	}
	return false
}

// EffectiveMode keeps existing capability JSON backward compatible by treating an empty mode as native.
func (c ProtocolCapability) EffectiveMode() ProtocolHandlingMode {
	if c.Mode == "" {
		return ProtocolHandlingModeNative
	}
	return c.Mode
}

// Validate validates protocol capability declarations stored on a channel.
func (p ChannelProtocolPolicy) Validate() error {
	if err := validateNativeProtocolCapabilities(p.Native, "channel"); err != nil {
		return err
	}
	if len(p.ModelOverrides) > MaxModelProtocolOverrides {
		return fmt.Errorf("model protocol overrides cannot exceed %d", MaxModelProtocolOverrides)
	}
	for model, profile := range p.ModelOverrides {
		trimmedModel := strings.TrimSpace(model)
		if trimmedModel == "" || trimmedModel != model || len(model) > 255 {
			return fmt.Errorf("invalid model protocol override: %s", model)
		}
		if err := validateNativeProtocolCapabilities(profile.Native, "model "+model); err != nil {
			return err
		}
	}
	quality := p.EffectiveMaxQuality()
	if quality != ProtocolConversionQualityGood && quality != ProtocolConversionQualityFair {
		return fmt.Errorf("invalid protocol conversion quality: %s", p.MaxQuality)
	}
	return nil
}

func validateNativeProtocolCapabilities(capabilities map[constant.EndpointType]ProtocolCapability, scope string) error {
	if len(capabilities) == 0 {
		return fmt.Errorf("%s native protocol capabilities are required", scope)
	}
	for endpointType, capability := range capabilities {
		if !IsTextProtocolEndpointType(endpointType) {
			return fmt.Errorf("invalid text protocol endpoint type: %s", endpointType)
		}
		if !capability.NonStream && !capability.Stream {
			return fmt.Errorf("protocol capability %s for %s must enable non-stream or stream", endpointType, scope)
		}
		mode := capability.EffectiveMode()
		if mode != ProtocolHandlingModeNative && mode != ProtocolHandlingModeNormalized {
			return fmt.Errorf("invalid protocol handling mode %s for %s in %s", capability.Mode, endpointType, scope)
		}
		if mode == ProtocolHandlingModeNormalized &&
			endpointType != constant.EndpointTypeAnthropic &&
			endpointType != constant.EndpointTypeOpenAIResponse {
			return fmt.Errorf("normalized protocol handling is unsupported for %s in %s", endpointType, scope)
		}
	}
	return nil
}

// IsTextProtocolEndpointType reports whether an endpoint belongs to the v1 text protocol policy.
func IsTextProtocolEndpointType(endpointType constant.EndpointType) bool {
	switch endpointType {
	case constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini:
		return true
	default:
		return false
	}
}

// ResolveSystemPrompt 返回指定原始模型应使用的渠道系统提示词，以及是否需要前置拼接客户端提示词。
func (s ChannelSettings) ResolveSystemPrompt(model string) (string, bool) {
	if prompt, ok := s.ModelSystemPrompts[model]; ok && strings.TrimSpace(prompt) != "" {
		return prompt, true
	}
	return s.SystemPrompt, s.SystemPromptOverride
}

// ResolveSystemPromptForAttempt 在兜底路由中优先保持客户端模型语义，再匹配实际尝试模型。
func (s ChannelSettings) ResolveSystemPromptForAttempt(requestedModel, attemptModel string, fallbackActive bool) (string, bool, string, string) {
	if prompt, ok := s.ModelSystemPrompts[requestedModel]; ok && strings.TrimSpace(prompt) != "" {
		return prompt, true, "model_requested", requestedModel
	}
	if fallbackActive && attemptModel != "" && attemptModel != requestedModel {
		if prompt, ok := s.ModelSystemPrompts[attemptModel]; ok && strings.TrimSpace(prompt) != "" {
			return prompt, true, "model_attempt", attemptModel
		}
	}
	return s.SystemPrompt, s.SystemPromptOverride, "channel_default", ""
}

// ResolveContextFallback 按初始路由模型精确查找渠道兜底规则。
func (s ChannelSettings) ResolveContextFallback(model string) (ModelContextFallback, bool) {
	rule, ok := s.ModelContextFallbacks[model]
	return rule, ok
}

// ValidateSystemPrompts 校验模型专属系统提示词配置。
func (s ChannelSettings) ValidateSystemPrompts() error {
	if len(s.ModelSystemPrompts) > MaxModelSystemPromptEntries {
		return fmt.Errorf("model system prompt entries cannot exceed %d", MaxModelSystemPromptEntries)
	}
	for model, prompt := range s.ModelSystemPrompts {
		trimmedModel := strings.TrimSpace(model)
		if trimmedModel == "" {
			return fmt.Errorf("model system prompt model cannot be empty")
		}
		if trimmedModel != model {
			return fmt.Errorf("model system prompt model cannot contain surrounding whitespace: %s", model)
		}
		if len(model) > 255 {
			return fmt.Errorf("model system prompt model is too long: %s", model)
		}
		if strings.TrimSpace(prompt) == "" {
			return fmt.Errorf("model system prompt cannot be empty: %s", model)
		}
		if len(prompt) > MaxModelSystemPromptBytes {
			return fmt.Errorf("model system prompt is too long: %s", model)
		}
	}
	return nil
}

// ValidateContextFallbacks 校验渠道模型上下文兜底规则。
func (s ChannelSettings) ValidateContextFallbacks() error {
	if len(s.ModelContextFallbacks) > MaxModelContextFallbacks {
		return fmt.Errorf("model context fallback entries cannot exceed %d", MaxModelContextFallbacks)
	}
	for sourceModel, rule := range s.ModelContextFallbacks {
		trimmedSource := strings.TrimSpace(sourceModel)
		if trimmedSource == "" || trimmedSource != sourceModel || len(sourceModel) > 255 {
			return fmt.Errorf("invalid model context fallback source model: %s", sourceModel)
		}
		if rule.SourceContextWindowTokens <= 0 {
			return fmt.Errorf("source context window must be positive: %s", sourceModel)
		}
		threshold := rule.EffectiveThresholdPercent()
		if threshold < 1 || threshold > 100 {
			return fmt.Errorf("context fallback threshold must be between 1 and 100: %s", sourceModel)
		}
		fallbackModel := strings.TrimSpace(rule.FallbackModel)
		if fallbackModel == "" || fallbackModel != rule.FallbackModel || len(fallbackModel) > 255 {
			return fmt.Errorf("invalid context fallback model: %s", sourceModel)
		}
		if fallbackModel == sourceModel {
			return fmt.Errorf("context fallback model must differ from source model: %s", sourceModel)
		}
		if rule.FallbackContextWindowTokens <= 0 {
			return fmt.Errorf("fallback context window must be positive: %s", sourceModel)
		}
		if rule.RouteMode != ContextFallbackModeSame && rule.RouteMode != ContextFallbackModeCross {
			return fmt.Errorf("invalid context fallback route mode: %s", sourceModel)
		}
		if rule.RouteMode == ContextFallbackModeSame && len(rule.TargetChannelIDs) > 0 {
			return fmt.Errorf("same-channel fallback cannot specify target channel ids: %s", sourceModel)
		}
		seenChannelIDs := make(map[int]struct{}, len(rule.TargetChannelIDs))
		for _, channelID := range rule.TargetChannelIDs {
			if channelID <= 0 {
				return fmt.Errorf("context fallback target channel id must be positive: %s", sourceModel)
			}
			if _, exists := seenChannelIDs[channelID]; exists {
				return fmt.Errorf("duplicate context fallback target channel id %d: %s", channelID, sourceModel)
			}
			seenChannelIDs[channelID] = struct{}{}
		}
	}
	return nil
}

type VertexKeyType string

const (
	VertexKeyTypeJSON   VertexKeyType = "json"
	VertexKeyTypeAPIKey VertexKeyType = "api_key"
)

type AwsKeyType string

const (
	AwsKeyTypeAKSK   AwsKeyType = "ak_sk" // 默认
	AwsKeyTypeApiKey AwsKeyType = "api_key"
)

type ChannelOtherSettings struct {
	AzureResponsesVersion                 string                `json:"azure_responses_version,omitempty"`
	VertexKeyType                         VertexKeyType         `json:"vertex_key_type,omitempty"` // "json" or "api_key"
	OpenRouterEnterprise                  *bool                 `json:"openrouter_enterprise,omitempty"`
	ClaudeBetaQuery                       bool                  `json:"claude_beta_query,omitempty"`          // Claude 渠道是否强制追加 ?beta=true
	AllowServiceTier                      bool                  `json:"allow_service_tier,omitempty"`         // 是否允许 service_tier 透传（默认过滤以避免额外计费）
	AllowInferenceGeo                     bool                  `json:"allow_inference_geo,omitempty"`        // 是否允许 inference_geo 透传（仅 Claude，默认过滤以满足数据驻留合规
	AllowSpeed                            bool                  `json:"allow_speed,omitempty"`                // 是否允许 speed 透传（仅 Claude，默认过滤以避免意外切换推理速度模式）
	AllowSafetyIdentifier                 bool                  `json:"allow_safety_identifier,omitempty"`    // 是否允许 safety_identifier 透传（默认过滤以保护用户隐私）
	DisableStore                          bool                  `json:"disable_store,omitempty"`              // 是否禁用 store 透传（默认允许透传，禁用后可能导致 Codex 无法使用）
	AllowIncludeObfuscation               bool                  `json:"allow_include_obfuscation,omitempty"`  // 是否允许 stream_options.include_obfuscation 透传（默认过滤以避免关闭流混淆保护）
	DisableTaskPollingSleep               bool                  `json:"disable_task_polling_sleep,omitempty"` // 是否跳过异步任务轮询间隔
	ConversationLogEnabled                bool                  `json:"conversation_log_enabled,omitempty"`   // 是否采集该渠道的完整文本对话
	AwsKeyType                            AwsKeyType            `json:"aws_key_type,omitempty"`
	UpstreamModelUpdateCheckEnabled       bool                  `json:"upstream_model_update_check_enabled,omitempty"`        // 是否检测上游模型更新
	UpstreamModelUpdateAutoSyncEnabled    bool                  `json:"upstream_model_update_auto_sync_enabled,omitempty"`    // 是否自动同步上游模型更新
	UpstreamModelUpdateLastCheckTime      int64                 `json:"upstream_model_update_last_check_time,omitempty"`      // 上次检测时间
	UpstreamModelUpdateLastDetectedModels []string              `json:"upstream_model_update_last_detected_models,omitempty"` // 上次检测到的可加入模型
	UpstreamModelUpdateLastRemovedModels  []string              `json:"upstream_model_update_last_removed_models,omitempty"`  // 上次检测到的可删除模型
	UpstreamModelUpdateIgnoredModels      []string              `json:"upstream_model_update_ignored_models,omitempty"`       // 手动忽略的模型
	AdvancedCustom                        *AdvancedCustomConfig `json:"advanced_custom,omitempty"`
}

func (s *ChannelOtherSettings) IsOpenRouterEnterprise() bool {
	if s == nil || s.OpenRouterEnterprise == nil {
		return false
	}
	return *s.OpenRouterEnterprise
}

const (
	advancedCustomConverterNone                        = "none"
	advancedCustomConverterClaudeMessagesToOpenAIChat  = "anthropic_messages_to_openai_chat_completions"
	advancedCustomConverterOpenAIChatToClaudeMessages  = "openai_chat_completions_to_anthropic_messages"
	advancedCustomConverterOpenAIChatToOpenAIResponses = "openai_chat_completions_to_openai_responses"
	advancedCustomConverterOpenAIResponsesToOpenAIChat = "openai_responses_to_openai_chat_completions"
	advancedCustomConverterOpenAIResponsesToGemini     = "openai_responses_to_gemini_generate_content"
	advancedCustomConverterGeminiContentToOpenAIChat   = "gemini_generate_content_to_openai_chat_completions"
	advancedCustomConverterOpenAIChatToGeminiContent   = "openai_chat_completions_to_gemini_generate_content"
)

const (
	AdvancedCustomAuthTypeNone   = "none"
	AdvancedCustomAuthTypeHeader = "header"
	AdvancedCustomAuthTypeQuery  = "query"
)

type AdvancedCustomConfig struct {
	Routes []AdvancedCustomRoute `json:"advanced_routes,omitempty"`
}

type AdvancedCustomRoute struct {
	IncomingPath string                   `json:"incoming_path,omitempty"`
	UpstreamPath string                   `json:"upstream_path,omitempty"`
	Converter    string                   `json:"converter,omitempty"`
	Models       []string                 `json:"models,omitempty"`
	Auth         *AdvancedCustomRouteAuth `json:"auth,omitempty"`
}

type AdvancedCustomRouteAuth struct {
	Type  string `json:"type,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

const (
	advancedCustomModelPlaceholder = "{model}"
	advancedCustomModelRegexPrefix = "re:"
)

const (
	advancedCustomEndpointPathOpenAIChat             = "/v1/chat/completions"
	advancedCustomEndpointPathOpenAIResponses        = "/v1/responses"
	advancedCustomEndpointPathOpenAIResponsesCompact = "/v1/responses/compact"
	advancedCustomEndpointPathClaudeMessages         = "/v1/messages"
	advancedCustomEndpointPathJinaRerank             = "/v1/rerank"
	advancedCustomEndpointPathImageGeneration        = "/v1/images/generations"
	advancedCustomEndpointPathEmbeddings             = "/v1/embeddings"
)

// MatchPath returns the first route whose IncomingPath matches requestPath.
// Matching mirrors the relay adaptor: exact match, {model} placeholder, and
// :generateContent <-> :streamGenerateContent equivalence.
func (c *AdvancedCustomConfig) MatchPath(requestPath string) (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	for _, route := range c.Routes {
		if matchAdvancedCustomIncomingPath(strings.TrimSpace(route.IncomingPath), requestPath) {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// MatchPathForModel returns the first route whose IncomingPath and Models match.
// An empty Models list is a catch-all fallback for that incoming path.
func (c *AdvancedCustomConfig) MatchPathForModel(requestPath string, model string) (AdvancedCustomRoute, bool) {
	if c == nil {
		return AdvancedCustomRoute{}, false
	}
	model = strings.TrimSpace(model)
	for _, route := range c.Routes {
		if matchAdvancedCustomIncomingPath(strings.TrimSpace(route.IncomingPath), requestPath) &&
			matchAdvancedCustomRouteModel(route.Models, model) {
			return route, true
		}
	}
	return AdvancedCustomRoute{}, false
}

// SupportsPath reports whether any route matches requestPath.
func (c *AdvancedCustomConfig) SupportsPath(requestPath string) bool {
	_, ok := c.MatchPath(requestPath)
	return ok
}

// SupportsPathForModel reports whether any route matches requestPath and model.
func (c *AdvancedCustomConfig) SupportsPathForModel(requestPath string, model string) bool {
	_, ok := c.MatchPathForModel(requestPath, model)
	return ok
}

func (c *AdvancedCustomConfig) SupportedEndpointTypesForModel(model string) []constant.EndpointType {
	if c == nil {
		return nil
	}
	model = strings.TrimSpace(model)
	endpoints := make([]constant.EndpointType, 0, len(c.Routes))
	seen := make(map[constant.EndpointType]struct{}, len(c.Routes))
	for _, route := range c.Routes {
		if !matchAdvancedCustomRouteModel(route.Models, model) {
			continue
		}
		endpointType, ok := advancedCustomEndpointTypeFromIncomingPath(strings.TrimSpace(route.IncomingPath))
		if !ok {
			continue
		}
		if _, exists := seen[endpointType]; exists {
			continue
		}
		seen[endpointType] = struct{}{}
		endpoints = append(endpoints, endpointType)
	}
	return endpoints
}

func advancedCustomEndpointTypeFromIncomingPath(incomingPath string) (constant.EndpointType, bool) {
	switch incomingPath {
	case advancedCustomEndpointPathOpenAIChat:
		return constant.EndpointTypeOpenAI, true
	case advancedCustomEndpointPathOpenAIResponses:
		return constant.EndpointTypeOpenAIResponse, true
	case advancedCustomEndpointPathOpenAIResponsesCompact:
		return constant.EndpointTypeOpenAIResponseCompact, true
	case advancedCustomEndpointPathClaudeMessages:
		return constant.EndpointTypeAnthropic, true
	case advancedCustomEndpointPathJinaRerank:
		return constant.EndpointTypeJinaRerank, true
	case advancedCustomEndpointPathImageGeneration:
		return constant.EndpointTypeImageGeneration, true
	case advancedCustomEndpointPathEmbeddings:
		return constant.EndpointTypeEmbeddings, true
	default:
		if isAdvancedCustomGeminiIncomingPath(incomingPath) {
			return constant.EndpointTypeGemini, true
		}
		return "", false
	}
}

func isAdvancedCustomGeminiIncomingPath(incomingPath string) bool {
	if !strings.HasPrefix(incomingPath, "/v1beta/models/") {
		return false
	}
	return strings.Contains(incomingPath, ":generateContent") || strings.Contains(incomingPath, ":streamGenerateContent")
}

func matchAdvancedCustomRouteModel(models []string, model string) bool {
	normalizedModels := normalizeAdvancedCustomRouteModels(models)
	if len(normalizedModels) == 0 {
		return true
	}
	for _, allowedModel := range normalizedModels {
		if matchAdvancedCustomRouteModelRule(allowedModel, model) {
			return true
		}
	}
	return false
}

// advancedCustomModelRegexCache caches compiled route model patterns. Route model
// matching runs on the request hot path (distributor affinity, ability filtering,
// channel cache filtering, adaptor resolve), so patterns must not be recompiled per
// request. Invalid patterns are cached as nil to avoid recompiling them as well.
var advancedCustomModelRegexCache sync.Map // pattern string -> *regexp.Regexp (nil when invalid)

func compileAdvancedCustomModelRegex(pattern string) *regexp.Regexp {
	if cached, ok := advancedCustomModelRegexCache.Load(pattern); ok {
		re, _ := cached.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = nil
	}
	advancedCustomModelRegexCache.Store(pattern, re)
	return re
}

func matchAdvancedCustomRouteModelRule(rule string, model string) bool {
	if !strings.HasPrefix(rule, advancedCustomModelRegexPrefix) {
		return rule == model
	}
	pattern := strings.TrimPrefix(rule, advancedCustomModelRegexPrefix)
	if pattern == "" {
		return false
	}
	re := compileAdvancedCustomModelRegex(pattern)
	return re != nil && re.MatchString(model)
}

func matchAdvancedCustomIncomingPath(configuredPath string, requestPath string) bool {
	if matchAdvancedCustomIncomingPathTemplate(configuredPath, requestPath) {
		return true
	}
	if strings.Contains(configuredPath, ":generateContent") {
		streamPath := strings.Replace(configuredPath, ":generateContent", ":streamGenerateContent", 1)
		return matchAdvancedCustomIncomingPathTemplate(streamPath, requestPath)
	}
	return false
}

func matchAdvancedCustomIncomingPathTemplate(configuredPath string, requestPath string) bool {
	if !strings.Contains(configuredPath, advancedCustomModelPlaceholder) {
		return configuredPath == requestPath
	}

	parts := strings.Split(configuredPath, advancedCustomModelPlaceholder)
	if len(parts) != 2 {
		return false
	}
	if !strings.HasPrefix(requestPath, parts[0]) || !strings.HasSuffix(requestPath, parts[1]) {
		return false
	}

	model := strings.TrimSuffix(strings.TrimPrefix(requestPath, parts[0]), parts[1])
	return model != "" && !strings.Contains(model, "/")
}

func IsAdvancedCustomConverterAllowed(converter string) bool {
	switch converter {
	case advancedCustomConverterNone,
		advancedCustomConverterClaudeMessagesToOpenAIChat,
		advancedCustomConverterOpenAIChatToClaudeMessages,
		advancedCustomConverterOpenAIChatToOpenAIResponses,
		advancedCustomConverterOpenAIResponsesToOpenAIChat,
		advancedCustomConverterOpenAIResponsesToGemini,
		advancedCustomConverterGeminiContentToOpenAIChat,
		advancedCustomConverterOpenAIChatToGeminiContent:
		return true
	default:
		return false
	}
}

func (c *AdvancedCustomConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("advanced_custom is required")
	}
	if len(c.Routes) == 0 {
		return fmt.Errorf("advanced_custom requires at least one route")
	}

	paths := make(map[string]*advancedCustomPathModelState, len(c.Routes))
	for i := range c.Routes {
		route := c.Routes[i]
		route.IncomingPath = strings.TrimSpace(route.IncomingPath)
		upstreamPath := strings.TrimSpace(route.UpstreamPath)
		route.Converter = strings.TrimSpace(route.Converter)
		if route.Converter == "" {
			route.Converter = advancedCustomConverterNone
		}

		if route.IncomingPath == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path is required", i)
		}
		if !strings.HasPrefix(route.IncomingPath, "/") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must start with /", i)
		}
		if strings.Contains(route.IncomingPath, "?") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].incoming_path must not include query", i)
		}
		if err := validateAdvancedCustomRouteModels(i, route.IncomingPath, route.Models, paths); err != nil {
			return err
		}

		if upstreamPath == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path is required", i)
		}
		if err := validateAdvancedCustomUpstreamTarget(i, upstreamPath); err != nil {
			return err
		}

		if !IsAdvancedCustomConverterAllowed(route.Converter) {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].converter is not registered: %s", i, route.Converter)
		}
		if err := validateAdvancedCustomConverterPath(i, route.IncomingPath, route.Converter); err != nil {
			return err
		}
		if err := validateAdvancedCustomRouteAuth(i, route.Auth); err != nil {
			return err
		}
	}

	return nil
}

type advancedCustomPathModelState struct {
	catchAllIndex int
	modelIndexes  map[string]int
}

func validateAdvancedCustomRouteModels(index int, incomingPath string, models []string, paths map[string]*advancedCustomPathModelState) error {
	state := paths[incomingPath]
	if state == nil {
		state = &advancedCustomPathModelState{
			catchAllIndex: -1,
			modelIndexes:  make(map[string]int),
		}
		paths[incomingPath] = state
	}

	normalizedModels := normalizeAdvancedCustomRouteModels(models)
	if len(normalizedModels) == 0 {
		if state.catchAllIndex >= 0 {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].models catch-all already exists for incoming_path: %s", index, incomingPath)
		}
		state.catchAllIndex = index
		return nil
	}

	if state.catchAllIndex >= 0 {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].models catch-all route must be last for incoming_path: %s", index, incomingPath)
	}

	seenInRoute := make(map[string]struct{}, len(normalizedModels))
	for _, model := range normalizedModels {
		if err := validateAdvancedCustomRouteModelRule(index, incomingPath, model); err != nil {
			return err
		}
		if _, exists := seenInRoute[model]; exists {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].models contains duplicate model for incoming_path %s: %s", index, incomingPath, model)
		}
		seenInRoute[model] = struct{}{}
		if existingIndex, exists := state.modelIndexes[model]; exists {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].models overlaps with advanced_routes[%d] for incoming_path %s: %s", index, existingIndex, incomingPath, model)
		}
		state.modelIndexes[model] = index
	}
	return nil
}

func validateAdvancedCustomRouteModelRule(index int, incomingPath string, model string) error {
	if !strings.HasPrefix(model, advancedCustomModelRegexPrefix) {
		return nil
	}
	pattern := strings.TrimPrefix(model, advancedCustomModelRegexPrefix)
	if pattern == "" {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].models regex is empty for incoming_path %s: %s", index, incomingPath, model)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].models regex is invalid for incoming_path %s: %s", index, incomingPath, model)
	}
	return nil
}

func normalizeAdvancedCustomRouteModels(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model != "" {
			normalized = append(normalized, model)
		}
	}
	return normalized
}

func validateAdvancedCustomUpstreamTarget(index int, upstreamPath string) error {
	if strings.HasPrefix(upstreamPath, "/") {
		if strings.HasPrefix(upstreamPath, "//") {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must be a full URL or a path starting with /", index)
		}
		return nil
	}

	parsedURL, err := url.Parse(upstreamPath)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must be a full URL or a path starting with /", index)
	}
	if !strings.EqualFold(parsedURL.Scheme, "http") && !strings.EqualFold(parsedURL.Scheme, "https") {
		return fmt.Errorf("advanced_custom.advanced_routes[%d].upstream_path must use http or https", index)
	}
	return nil
}

func validateAdvancedCustomConverterPath(index int, incomingPath string, converter string) error {
	switch converter {
	case advancedCustomConverterNone:
		return nil
	case advancedCustomConverterClaudeMessagesToOpenAIChat:
		if incomingPath == "/v1/messages" {
			return nil
		}
	case advancedCustomConverterOpenAIChatToClaudeMessages,
		advancedCustomConverterOpenAIChatToOpenAIResponses,
		advancedCustomConverterOpenAIChatToGeminiContent:
		if incomingPath == "/v1/chat/completions" {
			return nil
		}
	case advancedCustomConverterOpenAIResponsesToOpenAIChat:
		if incomingPath == "/v1/responses" {
			return nil
		}
	case advancedCustomConverterOpenAIResponsesToGemini:
		if incomingPath == "/v1/responses" {
			return nil
		}
	case advancedCustomConverterGeminiContentToOpenAIChat:
		if strings.Contains(incomingPath, ":generateContent") || strings.Contains(incomingPath, ":streamGenerateContent") {
			return nil
		}
	}
	return fmt.Errorf("advanced_custom.advanced_routes[%d].converter does not match incoming_path: %s", index, converter)
}

func validateAdvancedCustomRouteAuth(index int, auth *AdvancedCustomRouteAuth) error {
	if auth == nil {
		return nil
	}
	authType := strings.TrimSpace(auth.Type)
	switch authType {
	case AdvancedCustomAuthTypeNone:
		return nil
	case AdvancedCustomAuthTypeHeader, AdvancedCustomAuthTypeQuery:
		if strings.TrimSpace(auth.Name) == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.name is required", index)
		}
		if strings.TrimSpace(auth.Value) == "" {
			return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.value is required", index)
		}
		return nil
	default:
		return fmt.Errorf("advanced_custom.advanced_routes[%d].auth.type is invalid: %s", index, auth.Type)
	}
}
