package relaynormalize

import (
	"fmt"

	"github.com/QuantumNous/new-api/types"
)

const (
	RequestNormalizerAnthropicMessagesCompatible = "anthropic_messages_compatible"
	RequestNormalizerOpenAIResponsesCompatible   = "openai_responses_compatible"
)

type requestNormalizer func([]byte, types.RequestNormalizationOptions) ([]byte, types.ProtocolNormalizationAudit, error)
type requestValidator func([]byte, types.RequestNormalizationOptions) error

var requestNormalizers = map[string]requestNormalizer{
	RequestNormalizerAnthropicMessagesCompatible: normalizeAnthropicMessagesCompatible,
	RequestNormalizerOpenAIResponsesCompatible:   normalizeOpenAIResponsesCompatible,
}

var requestValidators = map[string]requestValidator{
	RequestNormalizerAnthropicMessagesCompatible: validateAnthropicMessagesCompatible,
	RequestNormalizerOpenAIResponsesCompatible:   validateOpenAIResponsesCompatible,
}

// NormalizeRequestByID applies one registered final-wire request normalizer.
func NormalizeRequestByID(normalizer string, body []byte) ([]byte, types.ProtocolNormalizationAudit, error) {
	return NormalizeRequestByIDWithOptions(normalizer, body, types.RequestNormalizationOptions{})
}

// NormalizeRequestByIDWithOptions applies one registered normalizer with channel-selected behavior.
func NormalizeRequestByIDWithOptions(normalizer string, body []byte, options types.RequestNormalizationOptions) ([]byte, types.ProtocolNormalizationAudit, error) {
	audit := types.ProtocolNormalizationAudit{Normalizer: normalizer}
	normalize, ok := requestNormalizers[normalizer]
	if !ok {
		return nil, audit, fmt.Errorf("request normalizer is not registered: %s", normalizer)
	}
	return normalize(body, options)
}

// ValidateRequestByID validates invariants owned by one registered request normalizer.
func ValidateRequestByID(normalizer string, body []byte) error {
	return ValidateRequestByIDWithOptions(normalizer, body, types.RequestNormalizationOptions{})
}

// ValidateRequestByIDWithOptions validates final-wire invariants selected by one channel capability.
func ValidateRequestByIDWithOptions(normalizer string, body []byte, options types.RequestNormalizationOptions) error {
	validate, ok := requestValidators[normalizer]
	if !ok {
		return fmt.Errorf("request normalizer validator is not registered: %s", normalizer)
	}
	return validate(body, options)
}
