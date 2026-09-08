package common

import "github.com/QuantumNous/new-api/dto"

// ContextTruncationState belongs to one upstream attempt, never the original request.
type ContextTruncationState struct {
	Enabled           bool                      `json:"enabled"`
	Rule              dto.ContextTruncationRule `json:"-"`
	Source            string                    `json:"source"`
	Model             string                    `json:"model"`
	Before            int                       `json:"before"`
	After             int                       `json:"after"`
	Budget            int                       `json:"budget"`
	RemovedTurns      int                       `json:"removed_turns"`
	RemovedMessages   int                       `json:"removed_messages"`
	Applied           bool                      `json:"applied"`
	Reason            string                    `json:"reason,omitempty"`
	Billed            int                       `json:"billed,omitempty"`
	Upstream          int                       `json:"upstream,omitempty"`
	Reported          int64                     `json:"reported,omitempty"`
	ClientUsageReason string                    `json:"client_usage_reason,omitempty"`
	clientUsage       clientContextUsage
}
