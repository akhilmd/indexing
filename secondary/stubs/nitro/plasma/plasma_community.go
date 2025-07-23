//go:build community
// +build community

package plasma

import (
	"fmt"
	"net/http"
)

type StubType int

var Diag StubType

type MemTunerConfig = bool
type MemTunerDistStats = bool

func SetMemoryQuota(_ int64, _ bool) {
}

func GetMandatoryQuota() (int64, int64) {
	return 0, 0
}

func GetWorkingSetSize() int64 {
	return 0
}

func SetLogReclaimBlockSize(_ int64) {
}

func MemoryInUse() int64 {
	return 0
}

func GolangMemoryInUse() int64 {
	return 0
}

func TenantQuotaNeeded() int64 {
	return 0
}

func MakeMemTunerConfig(qsp, mqt, mqdd, qmsp int64) MemTunerConfig {
	return false
}

func MakeMemTunerDistStats(nb, plbp, blbp int64, plct, blct time.Time) MemTunerDistStats {
	return false
}

func RunMemQuotaTuner(
	quotaDistCh chan bool,
	getAssignedQuota func() int64,
	getConfig func() MemTunerConfig,
	getDistStats func() MemTunerDistStats,
) {
}

func (d *StubType) HandleHttp(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "not implemented")
}
