package dto

// ModelRateLimitResponse defines the public response returned for a user-group rate-limit rejection.
type ModelRateLimitResponse struct {
	StatusCode   int    `json:"status_code"`
	ErrorMessage string `json:"error_message"`
}

// UserModelRateLimitResponseConfig stores the shared delay and response fallback hierarchy.
type UserModelRateLimitResponseConfig struct {
	DelaySeconds    int                               `json:"delay_seconds"`
	DefaultResponse ModelRateLimitResponse            `json:"default_response"`
	GroupResponses  map[string]ModelRateLimitResponse `json:"group_responses"`
}

// UserModelRateLimitRuleRequest is the shared create and update payload for a user-group rule.
type UserModelRateLimitRuleRequest struct {
	UserId       int                     `json:"user_id"`
	Group        string                  `json:"group"`
	TotalCount   int                     `json:"total_count"`
	SuccessCount int                     `json:"success_count"`
	Response     *ModelRateLimitResponse `json:"response"`
}
