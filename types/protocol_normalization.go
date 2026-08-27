package types

// ProtocolNormalizationAudit records payload-free request normalization results.
type ProtocolNormalizationAudit struct {
	Normalizer                    string `json:"normalizer"`
	ReasoningOnlyAssistantDropped int    `json:"reasoning_only_assistant_dropped,omitempty"`
	ToolIDsNormalized             int    `json:"tool_ids_normalized,omitempty"`
	ToolIDCollisions              int    `json:"tool_id_collisions,omitempty"`
	OrphanToolResultIDs           int    `json:"orphan_tool_result_ids,omitempty"`
}
