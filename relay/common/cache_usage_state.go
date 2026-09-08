package common

import (
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"math/rand/v2"
)

// CacheUsageState holds immutable settings and monotonic raw upstream facts for one attempt.
type CacheUsageState struct {
	Policy   dto.CacheUsageSimulationPolicy `json:"-"`
	Samples  [2]int                         `json:"-"`
	Source   string                         `json:"source"`
	Presence string                         `json:"presence"`
	HasInput bool                           `json:"-"`
	Reason   string                         `json:"reason,omitempty"`
	Applied  bool                           `json:"applied"`
	Creation int                            `json:"creation"`
	Read     int                            `json:"read"`
	Billed   int                            `json:"billed"`
}

// InitInputPolicyState resets only attempt state; the request's independent samples survive retry.
func (info *RelayInfo) InitInputPolicyState() {
	info.ContextTruncation = nil
	info.ValidateInputPolicyBody = nil
	info.CacheUsageSimulation = nil
	p, source, enabled := model_setting.ResolveCacheUsageSimulation(info.ChannelOtherSettings.CacheUsageSimulation)
	if !enabled {
		return
	}
	if info.CacheUsageSamples == nil {
		samples := [2]int{rand.IntN(100), rand.IntN(100)}
		info.CacheUsageSamples = &samples
	}
	info.CacheUsageSimulation = &CacheUsageState{Policy: p, Source: source, Samples: *info.CacheUsageSamples, Presence: "unknown"}
}
