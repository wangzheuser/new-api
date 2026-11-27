package dto

// AgentHeaderPreset supplies an upstream identity for callers without a matching client identity.
type AgentHeaderPreset struct {
	Client  string            `json:"client"`
	Headers map[string]string `json:"headers"`
}

func (p AgentHeaderPreset) IdentityHeaderNames() []string {
	switch p.Client {
	case "codex":
		return []string{"user-agent", "originator", "version"}
	case "claude":
		return []string{"user-agent", "x-app", "anthropic-version", "anthropic-beta"}
	default:
		return nil
	}
}
