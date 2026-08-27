package relaynormalize

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ClaudeToolIDNormalizer keeps tool call and result identifiers consistent within one request.
type ClaudeToolIDNormalizer struct {
	mappings map[string]string
	owners   map[string]string
	count    int
}

// NewClaudeToolIDNormalizer creates an empty request-scoped identifier normalizer.
func NewClaudeToolIDNormalizer() *ClaudeToolIDNormalizer {
	return &ClaudeToolIDNormalizer{
		mappings: make(map[string]string),
		owners:   make(map[string]string),
	}
}

// Normalize returns a deterministic Claude-compatible identifier and reports changes and new collisions.
func (n *ClaudeToolIDNormalizer) Normalize(original string) (normalized string, changed bool, collision bool) {
	if existing, ok := n.mappings[original]; ok {
		return existing, existing != original, false
	}

	base := normalizeClaudeToolIDBase(original)
	if base == "" {
		base = "toolu_" + shortSHA256(original, 12)
	}
	normalized = base
	if owner, exists := n.owners[normalized]; exists && owner != original {
		collision = true
		n.count++
		hash := shortSHA256(original, 8)
		normalized = base + "_" + hash
		// A pre-existing legal identifier can equal the first collision candidate.
		// Extend the same digest deterministically rather than emitting a duplicate.
		for length := 12; ; length += 4 {
			owner, exists = n.owners[normalized]
			if !exists || owner == original {
				break
			}
			if length > 64 {
				normalized = base + "_" + hash + "_" + shortSHA256(base+original, 8)
				break
			}
			normalized = base + "_" + shortSHA256(original, length)
		}
	}

	n.mappings[original] = normalized
	n.owners[normalized] = original
	return normalized, normalized != original, collision
}

// Collisions returns the number of distinct base identifier collisions resolved so far.
func (n *ClaudeToolIDNormalizer) Collisions() int {
	if n == nil {
		return 0
	}
	return n.count
}

func normalizeClaudeToolIDBase(original string) string {
	var builder strings.Builder
	builder.Grow(len(original))
	inInvalidRun := false
	for i := 0; i < len(original); i++ {
		char := original[i]
		if isClaudeToolIDByte(char) {
			builder.WriteByte(char)
			inInvalidRun = false
			continue
		}
		if !inInvalidRun {
			builder.WriteByte('_')
			inInvalidRun = true
		}
	}
	return builder.String()
}

func isClaudeToolIDByte(char byte) bool {
	return char >= 'a' && char <= 'z' ||
		char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' ||
		char == '_' || char == '-'
}

func shortSHA256(value string, length int) string {
	digest := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(digest[:])
	if length > len(encoded) {
		length = len(encoded)
	}
	return encoded[:length]
}
