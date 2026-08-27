package relaynormalize

import (
	"fmt"

	"github.com/QuantumNous/new-api/types"
)

const RequestNormalizerAnthropicMessagesCompatible = "anthropic_messages_compatible"

type requestNormalizer func([]byte) ([]byte, types.ProtocolNormalizationAudit, error)
type requestValidator func([]byte) error

var requestNormalizers = map[string]requestNormalizer{
	RequestNormalizerAnthropicMessagesCompatible: normalizeAnthropicMessagesCompatible,
}

var requestValidators = map[string]requestValidator{
	RequestNormalizerAnthropicMessagesCompatible: validateAnthropicMessagesCompatible,
}

// NormalizeRequestByID applies one registered final-wire request normalizer.
func NormalizeRequestByID(normalizer string, body []byte) ([]byte, types.ProtocolNormalizationAudit, error) {
	audit := types.ProtocolNormalizationAudit{Normalizer: normalizer}
	normalize, ok := requestNormalizers[normalizer]
	if !ok {
		return nil, audit, fmt.Errorf("request normalizer is not registered: %s", normalizer)
	}
	return normalize(body)
}

// ValidateRequestByID validates invariants owned by one registered request normalizer.
func ValidateRequestByID(normalizer string, body []byte) error {
	validate, ok := requestValidators[normalizer]
	if !ok {
		return fmt.Errorf("request normalizer validator is not registered: %s", normalizer)
	}
	return validate(body)
}
