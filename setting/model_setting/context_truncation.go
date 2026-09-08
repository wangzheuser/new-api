package model_setting

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"sync/atomic"
)

const ContextTruncationOption = "context_truncation.policy"

var truncationPolicy atomic.Pointer[dto.ContextTruncationPolicy]

// ParseContextTruncation validates a complete policy without changing runtime state.
func ParseContextTruncation(value string) (*dto.ContextTruncationPolicy, error) {
	if len(value) > 65535 {
		return nil, fmt.Errorf("context_truncation: policy exceeds 64 KiB")
	}
	var p *dto.ContextTruncationPolicy
	if err := common.UnmarshalJsonStr(value, &p); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("context_truncation: policy must be an object")
	}
	return p, p.Validate(false)
}

// SetContextTruncation publishes one immutable policy; invalid persisted data disables this feature.
func SetContextTruncation(value string) error {
	p, err := ParseContextTruncation(value)
	if err != nil {
		truncationPolicy.Store(&dto.ContextTruncationPolicy{ForceDisabled: true})
		return err
	}
	truncationPolicy.Store(p)
	return nil
}

// ResolveContextTruncation snapshots the rule for this attempt, honoring an emergency stop.
func ResolveContextTruncation(channel *dto.ContextTruncationPolicy, model string) (dto.ContextTruncationRule, string, bool) {
	p := truncationPolicy.Load()
	if p != nil && p.ForceDisabled {
		return dto.ContextTruncationRule{}, "force_disabled", false
	}
	if channel != nil {
		if r, ok := channel.Models[model]; ok && r.Mode != "inherit" && r.Mode != "" {
			return r, "channel", r.Mode == "custom" && r.Validate(true) == nil
		}
	}
	if p != nil {
		if r, ok := p.Models[model]; ok {
			return r, "global", r.Mode == "custom"
		}
	}
	return dto.ContextTruncationRule{}, "disabled", false
}
