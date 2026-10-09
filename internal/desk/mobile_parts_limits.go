package desk

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
)

type mobileFinalizeLimits struct{ global, perOwner int }

func mobileFinalizerLimits() mobileFinalizeLimits {
	cpus, memory := mobileFinalizeResources(os.ReadFile, min(runtime.NumCPU(), runtime.GOMAXPROCS(0)))
	return resolveMobileFinalizerLimits(cpus, memory, os.Getenv)
}

// Invalid overrides fall back to host defaults. Hard ceilings also bound the
// memory and storage pressure from explicitly configured concurrency.
func resolveMobileFinalizerLimits(cpus int, memory int64, getenv func(string) string) mobileFinalizeLimits {
	// Budget roughly 2 GiB of visible RAM per concurrent index writer.
	memoryWorkers := int(min(int64(8), max(int64(1), memory/(2<<30))))
	limit := mobileFinalizeLimits{min(memoryWorkers, max(1, cpus/2)), min(4, memoryWorkers, max(1, cpus/4))}
	read := func(name string, fallback, ceiling int) int {
		n, err := strconv.Atoi(getenv(name))
		if err != nil || n < 1 || n > ceiling {
			return fallback
		}
		return n
	}
	limit.global = read("WEAZLCLOUD_MOBILE_FINALIZE_WORKERS", limit.global, 8)
	limit.perOwner = min(limit.global, read("WEAZLCLOUD_MOBILE_FINALIZE_WORKERS_PER_OWNER", limit.perOwner, 4))
	return limit
}

// ValidateMobileFinalizeSettings can be called by server startup before serving.
func ValidateMobileFinalizeSettings() error {
	for name, ceiling := range map[string]int{
		"WEAZLCLOUD_MOBILE_FINALIZE_WORKERS":           8,
		"WEAZLCLOUD_MOBILE_FINALIZE_WORKERS_PER_OWNER": 4,
	} {
		raw := os.Getenv(name)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > ceiling {
			return fmt.Errorf("%s must be an integer from 1 to %d", name, ceiling)
		}
	}
	return nil
}

func (h *Handler) mobileFinalizerLimits() mobileFinalizeLimits {
	h.mobileFinalizeOnce.Do(func() { h.mobileFinalizeConfig = mobileFinalizerLimits() })
	return h.mobileFinalizeConfig
}
