package desk

import (
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"os"
	"runtime"
)

func mobileReceiveLimits() mobileparts.ReceiveLimits {
	cpus, memory := mobileFinalizeResources(os.ReadFile, min(runtime.NumCPU(), runtime.GOMAXPROCS(0)))
	return resolveMobileReceiveLimits(cpus, memory)
}
func resolveMobileReceiveLimits(cpus int, memory int64) mobileparts.ReceiveLimits {
	perOwner := min(8, max(2, cpus), int(max(int64(2), memory/(256<<20))))
	return mobileparts.ReceiveLimits{
		Global: min(32, perOwner*2), PerOwner: perOwner,
		Pending: 512, PendingBytes: min(int64(32<<30), max(int64(1<<30), memory/4)),
	}
}
