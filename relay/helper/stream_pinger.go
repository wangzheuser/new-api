package helper

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// EnsureConfiguredStreamPinger starts the request pinger when the runtime setting enables it.
func EnsureConfiguredStreamPinger(c *gin.Context, info *relaycommon.RelayInfo) {
	settings := operation_setting.GetGeneralSetting()
	if !settings.PingIntervalEnabled {
		return
	}
	EnsureStreamPinger(
		c,
		info,
		time.Duration(settings.PingIntervalSeconds)*time.Second,
	)
}

// EnsureStreamPinger starts the single APP-to-client pinger for this relay request.
func EnsureStreamPinger(c *gin.Context, info *relaycommon.RelayInfo, interval time.Duration) {
	if c == nil || info == nil || info.HasStreamPinger() || info.DisablePing {
		return
	}
	if interval <= 0 {
		interval = DefaultPingInterval
	}
	pingerCtx, stop := context.WithCancel(c.Request.Context())
	done := make(chan struct{})
	info.SetStreamPinger(stop, done)

	gopool.Go(func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		writeMutex := info.StreamWriterMutex()
		for {
			select {
			case <-ticker.C:
				writeMutex.Lock()
				ExtendWriteDeadline(c)
				err := PingData(c)
				writeMutex.Unlock()
				if err != nil {
					if info.StreamStatus != nil {
						info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPingFail, err)
					}
					info.CancelStreamUpstream()
					logger.LogError(c, "SSE ping flush failed: "+err.Error())
					return
				}
				logger.LogDebug(c, "SSE ping flushed")
			case <-pingerCtx.Done():
				return
			}
		}
	})
}
