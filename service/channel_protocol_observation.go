package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/hot"
)

type protocolObservation struct {
	Supported bool `json:"supported"`
}

var protocolObservations = hot.NewHotCache[string, protocolObservation](hot.LRU, 10000).WithTTL(time.Hour).Build()

const protocolAttemptContext = "protocol_attempts"

// ProtocolRequestReplayable rejects provider-owned conversation and sealed reasoning state.
func ProtocolRequestReplayable(request any) bool {
	data, err := common.Marshal(request)
	if err != nil {
		return false
	}
	var value any
	if common.Unmarshal(data, &value) != nil {
		return false
	}
	return protocolValueReplayable(value)
}

func protocolValueReplayable(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		kind, _ := v["type"].(string)
		if kind == "redacted_thinking" || kind == "item_reference" {
			return false
		}
		for key, item := range v {
			switch key {
			case "parameters", "input_schema", "responseSchema", "response_schema", "schema", "metadata", "arguments", "args":
				continue // Client-defined schemas and tool arguments contain arbitrary field names.
			case "input":
				if kind == "tool_use" {
					continue
				}
			case "previous_response_id", "conversation", "encrypted_content", "thoughtSignature", "thought_signature", "signature", "redacted_thinking",
				"cachedContent", "cached_content", "file_id", "container_id":
				if item != nil && item != "" {
					return false
				}
			}
			if !protocolValueReplayable(item) {
				return false
			}
		}
	case []any:
		for _, item := range v {
			if !protocolValueReplayable(item) {
				return false
			}
		}
	}
	return true
}

// TextReplaySafety checks the original JSON once, including passthrough-only fields.
func TextReplaySafety(c *gin.Context) (portable, safeAfterTimeout bool) {
	const key = "text_request_replay_safety"
	type safety struct{ portable, afterTimeout bool }
	if result, ok := common.GetContextKeyType[safety](c, key); ok {
		return result.portable, result.afterTimeout
	}
	storage, ok := common.GetContextKeyType[common.BodyStorage](c, common.KeyBodyStorage)
	if !ok {
		return false, false
	}
	defer func() { c.Set(key, safety{portable, safeAfterTimeout}) }()
	reader, err := storage.NewReader()
	if err != nil {
		return false, false
	}
	defer reader.Close()
	var request map[string]any
	if common.DecodeJson(reader, &request) != nil || request == nil || !protocolValueReplayable(request) {
		return false, false
	}
	// A timeout cannot establish whether hosted tools, background jobs or media generation already ran.
	if background, _ := request["background"].(bool); background {
		return true, false
	}
	if tools, ok := request["tools"].([]any); ok {
		for _, item := range tools {
			tool, _ := item.(map[string]any)
			kind, _ := tool["type"].(string)
			if kind == "function" || kind == "custom" || (kind == "" && tool["name"] != nil && tool["input_schema"] != nil) {
				continue
			}
			if len(tool) == 1 && (tool["functionDeclarations"] != nil || tool["function_declarations"] != nil) {
				continue
			}
			return true, false
		}
	}
	sets := []any{request["modalities"]}
	for _, key := range []string{"generationConfig", "generation_config"} {
		if config, ok := request[key].(map[string]any); ok {
			sets = append(sets, config["responseModalities"], config["response_modalities"])
		}
	}
	for _, set := range sets {
		values, _ := set.([]any)
		for _, value := range values {
			modality, _ := value.(string)
			if !strings.EqualFold(modality, "text") {
				return true, false
			}
		}
	}
	return true, true
}

// ProtocolEndpointRefused only recognizes explicit endpoint refusals before status mapping.
func ProtocolEndpointRefused(apiErr *types.NewAPIError) bool {
	if apiErr == nil {
		return false
	}
	status, real := apiErr.GetUpstreamStatusCode()
	if !real || (status != http.StatusBadRequest && status != http.StatusNotFound && status != http.StatusMethodNotAllowed && status != http.StatusUnprocessableEntity) {
		return false
	}
	message := strings.ToLower(apiErr.Error())
	for _, transient := range []string{"quota", "credit", "rate limit", "permission", "access", "max_tokens", "context length", "tools", "image"} {
		if strings.Contains(message, transient) {
			return false
		}
	}
	for _, refusal := range []string{"unsupported_endpoint", "endpoint_not_supported", "does not support this endpoint", "not supported on this endpoint", "not supported in this endpoint", "only supported in /v1/responses", "only supported on /v1/responses", "use the /v1/responses", "not a chat model and thus not supported", "不支持此接口", "不支持该接口"} {
		if strings.Contains(message, refusal) {
			return true
		}
	}
	return false
}

// ProtocolObservationKey isolates credentials, aliases with different policies, and wire-affecting configuration.
func ProtocolObservationKey(channel *model.Channel, modelName, key string, stream bool, headers ...http.Header) string {
	settings := channel.GetSetting()
	if settings.ProtocolPolicy == nil {
		return ""
	}
	native, _ := settings.ProtocolPolicy.NativeForModel(modelName)
	mapped, err := common.ResolveMappedModel(channel.GetModelMapping(), modelName)
	if err != nil {
		return ""
	}
	var resolvedHeaders []any
	overrideData, err := common.Marshal(channel.GetHeaderOverride())
	if err != nil {
		return ""
	}
	overrides := string(overrideData)
	for {
		_, tail, found := strings.Cut(overrides, "{client_header:")
		if !found {
			break
		}
		name, rest, found := strings.Cut(tail, "}")
		if !found {
			break
		}
		value := ""
		if len(headers) > 0 {
			value = headers[0].Get(name)
		}
		resolvedHeaders = append(resolvedHeaders, name, value)
		overrides = rest
	}
	data, err := common.Marshal([]any{channel.GetBaseURL(), channel.GetModelMapping(), native, settings.ProtocolPolicy.AutoConvert, settings.ProtocolPolicy.MaxQuality, channel.GetParamOverride(), channel.GetHeaderOverride(), settings, channel.GetOtherSettings(), resolvedHeaders})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("newapi:protocol-observation:v1:%d:%s:%x:%x:%t", channel.Id, MultiKeyFingerprint(key), sha256.Sum256(data), sha256.Sum256([]byte(mapped)), stream)
}

func readProtocolObservation(key string) (protocolObservation, bool) {
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		raw, err := common.RDB.Get(ctx, key).Result()
		var value protocolObservation
		if err == nil && common.UnmarshalJsonStr(raw, &value) == nil {
			return value, true
		}
		return value, false
	}
	value, found, _ := protocolObservations.Get(key)
	return value, found
}

// RecordProtocolObservation never changes administrator-owned channel settings.
func RecordProtocolObservation(key string, endpoint constant.EndpointType, supported bool) {
	if key == "" {
		return
	}
	key += ":" + string(endpoint)
	if previous, found := readProtocolObservation(key); found && previous.Supported == supported {
		return
	}
	ttl := 10 * time.Minute
	if supported {
		ttl = time.Hour
	}
	value := protocolObservation{Supported: supported}
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		data, err := common.Marshal(value)
		if err == nil {
			_ = common.RDB.Set(ctx, key, data, ttl).Err()
		}
		return
	}
	protocolObservations.SetWithTTL(key, value, ttl)
}

// PlanObservedProtocolRoute keeps static declarations authoritative and observations temporary.
func PlanObservedProtocolRoute(c *gin.Context, channel *model.Channel, modelName, key string, stream bool) (*types.ChannelRoutePlan, string, error) {
	if _, _, _, textProtocol := ResolveClientTextProtocol(c.Request.URL.Path); !textProtocol {
		return nil, "", nil
	}
	settings := channel.GetSetting()
	policy := settings.ProtocolPolicy
	if channel.Type != constant.ChannelTypeOpenAI || policy == nil || !policy.RuntimeFallback || !policy.AutoConvert || settings.PassThroughBodyEnabled {
		return nil, "", nil
	}
	observationKey := ProtocolObservationKey(channel, modelName, key, stream, c.Request.Header)
	if observationKey == "" {
		return nil, "", nil
	}
	value, _ := c.Get(protocolAttemptContext)
	tried, _ := value.(map[string]map[constant.EndpointType]bool)
	if tried == nil {
		tried = make(map[string]map[constant.EndpointType]bool)
		c.Set(protocolAttemptContext, tried)
	}
	if tried[observationKey] == nil {
		tried[observationKey] = make(map[constant.EndpointType]bool)
	}
	excluded := make(map[constant.EndpointType]bool)
	for endpoint := range tried[observationKey] {
		excluded[endpoint] = true
	}
	native, _ := policy.NativeForModel(modelName)
	for _, descriptor := range textProtocolDescriptors {
		if !protocolCapabilitySupports(native[descriptor.EndpointType], stream) {
			continue
		}
		if observation, found := readProtocolObservation(observationKey + ":" + string(descriptor.EndpointType)); found && !observation.Supported {
			excluded[descriptor.EndpointType] = true
		}
	}
	plan, err := planChannelProtocolRouteExcluding(channel.Type, settings, modelName, c.Request.URL.Path, stream, excluded)
	if err != nil {
		return nil, observationKey, err
	}
	if plan == nil {
		// Revalidate stale observations, but never repeat a path already attempted by this request.
		plan, err = planChannelProtocolRouteExcluding(channel.Type, settings, modelName, c.Request.URL.Path, stream, tried[observationKey])
	}
	if plan != nil {
		plan.SelectionReason = "configured_protocol"
		if len(tried[observationKey]) > 0 {
			plan.SelectionReason = "protocol_fallback"
		} else if len(excluded) > 0 {
			plan.SelectionReason = "protocol_observation"
		}
	}
	return plan, observationKey, err
}

func MarkProtocolAttempt(c *gin.Context, key string, endpoint constant.EndpointType) {
	value, _ := c.Get(protocolAttemptContext)
	if tried, ok := value.(map[string]map[constant.EndpointType]bool); ok && tried[key] != nil {
		tried[key][endpoint] = true
	}
}

func ClearProtocolObservations(ctx context.Context, channelID int) error {
	prefix := fmt.Sprintf("newapi:protocol-observation:v1:%d:", channelID)
	if common.RedisEnabled && common.RDB != nil {
		var cursor uint64
		for {
			keys, next, err := common.RDB.Scan(ctx, cursor, prefix+"*", 100).Result()
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err := common.RDB.Del(ctx, keys...).Err(); err != nil {
					return err
				}
			}
			cursor = next
			if cursor == 0 {
				return nil
			}
		}
	}
	for _, key := range protocolObservations.Keys() {
		if strings.HasPrefix(key, prefix) {
			protocolObservations.Delete(key)
		}
	}
	return nil
}
