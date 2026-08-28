package types

type ReasoningHistoryPolicy string

const (
	ReasoningHistoryPolicyPreserve ReasoningHistoryPolicy = "preserve"
	ReasoningHistoryPolicyStrip    ReasoningHistoryPolicy = "strip"
)

// RequestNormalizationOptions contains payload-free behavior selected by one channel capability.
type RequestNormalizationOptions struct {
	ReasoningHistoryPolicy ReasoningHistoryPolicy `json:"reasoning_history_policy,omitempty"`
}

// ProtocolNormalizationAudit records payload-free request normalization results.
type ProtocolNormalizationAudit struct {
	Normalizer                          string `json:"normalizer"`
	ReasoningAssistantMessagesPreserved int    `json:"reasoning_assistant_messages_preserved,omitempty"`
	ReasoningBlocksDropped              int    `json:"reasoning_blocks_dropped,omitempty"`
	ReasoningOnlyAssistantDropped       int    `json:"reasoning_only_assistant_dropped,omitempty"`
	EmptyAssistantMessagesDropped       int    `json:"empty_assistant_messages_dropped,omitempty"`
	ToolIDsNormalized                   int    `json:"tool_ids_normalized,omitempty"`
	ToolIDCollisions                    int    `json:"tool_id_collisions,omitempty"`
	OrphanToolResultIDs                 int    `json:"orphan_tool_result_ids,omitempty"`
}
