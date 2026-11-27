package dto

import (
	"fmt"
	"slices"
	"strings"

	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"golang.org/x/net/http/httpguts"
)

type AgentHeaderPreset = kitdto.AgentHeaderPreset

func (s ChannelSettings) ValidateAgentHeaderPreset() error {
	p := s.AgentHeaderPreset
	if p == nil {
		return nil
	}
	names := p.IdentityHeaderNames()
	if len(names) == 0 {
		return fmt.Errorf("unsupported agent header preset client")
	}
	normalized := make(map[string]string, len(p.Headers))
	for name, value := range p.Headers {
		name = strings.ToLower(strings.TrimSpace(name))
		if !slices.Contains(names, name) {
			return fmt.Errorf("unsupported agent preset header: %s", name)
		}
		if _, exists := normalized[name]; exists {
			return fmt.Errorf("duplicate agent preset header: %s", name)
		}
		if !httpguts.ValidHeaderFieldValue(value) || strings.TrimSpace(value) == "" {
			return fmt.Errorf("invalid agent preset header value: %s", name)
		}
		normalized[name] = value
	}
	if normalized["user-agent"] == "" {
		return fmt.Errorf("agent header preset requires User-Agent")
	}
	p.Headers = normalized
	return nil
}
