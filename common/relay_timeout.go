package common

import (
	"fmt"
	"math"
	"strconv"
	"sync/atomic"
	"time"
)

const RelayFirstPayloadTimeoutOption = "RelayFirstPayloadTimeout"
const DefaultRelayFirstPayloadTimeout = 180
const MaxRelayFirstPayloadTimeoutSeconds = math.MaxInt64 / int64(time.Second)

var relayFirstPayloadTimeout atomic.Int64

func init() {
	SetRelayFirstPayloadTimeout(DefaultRelayFirstPayloadTimeout * time.Second)
}

func GetRelayFirstPayloadTimeout() time.Duration {
	return time.Duration(relayFirstPayloadTimeout.Load())
}

func SetRelayFirstPayloadTimeout(timeout time.Duration) {
	relayFirstPayloadTimeout.Store(int64(timeout))
}

func ParseRelayFirstPayloadTimeout(value string) (time.Duration, error) {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 || seconds > MaxRelayFirstPayloadTimeoutSeconds {
		return 0, fmt.Errorf("Enter whole seconds between 0 and %d", MaxRelayFirstPayloadTimeoutSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}
