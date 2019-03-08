package common

import (
	"sync"
	"time"

	// "github.com/akhilmd/ntp"
)

var lastKnownUTCTime time.Time
var lastKnownOffset time.Duration
var updateLock sync.RWMutex

// returns nanoseconds since unix epoch
func CalcAbsNow() int64 {
	updateLock.RLock()
	defer updateLock.RUnlock()

	currentTime := time.Now().Add(lastKnownOffset)
	monoDiff := currentTime.Sub(lastKnownUTCTime)
	calculatedAbsTimestamp := lastKnownUTCTime.Add(monoDiff).UnixNano()

	return calculatedAbsTimestamp
}

func UpdateLastKnownUTCTime() {
	// avoid pinging NTP server during developement
	// response, _ := ntp.Query("time.apple.com")
	// responseRecvTime := response.DestTime.Local()

	responseRecvTime := time.Now()

	updateLock.Lock()
	defer updateLock.Unlock()

	lastKnownOffset = 0
	// lastKnownOffset = response.ClockOffset
	lastKnownUTCTime = responseRecvTime.Add(lastKnownOffset)
}
