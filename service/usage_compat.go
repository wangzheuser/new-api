package service

import (
	"github.com/QuantumNous/new-api/common"
	rootdto "github.com/QuantumNous/new-api/dto"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
)

// relayUsage normalizes the two DTO copies kept at the host/module boundary.
func relayUsage(value any) *relaydto.Usage {
	switch usage := value.(type) {
	case nil:
		return nil
	case *relaydto.Usage:
		return usage
	case relaydto.Usage:
		copy := usage
		return &copy
	case *rootdto.Usage:
		if usage == nil {
			return nil
		}
		encoded, err := common.Marshal(*usage)
		if err != nil {
			return nil
		}
		var converted relaydto.Usage
		if err := common.Unmarshal(encoded, &converted); err != nil {
			return nil
		}
		converted.InputPolicyAdjusted = usage.InputPolicyAdjusted
		return &converted
	case rootdto.Usage:
		return relayUsage(&usage)
	default:
		return nil
	}
}

// rootUsage converts relaykit usage to the host DTO used by legacy billing helpers.
func rootUsage(value any) *rootdto.Usage {
	switch usage := value.(type) {
	case nil:
		return nil
	case *rootdto.Usage:
		return usage
	case rootdto.Usage:
		copy := usage
		return &copy
	case *relaydto.Usage:
		if usage == nil {
			return nil
		}
		encoded, err := common.Marshal(*usage)
		if err != nil {
			return nil
		}
		var converted rootdto.Usage
		if err := common.Unmarshal(encoded, &converted); err != nil {
			return nil
		}
		converted.InputPolicyAdjusted = usage.InputPolicyAdjusted
		return &converted
	case relaydto.Usage:
		return rootUsage(&usage)
	default:
		return nil
	}
}
