package library

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PreviewResources describes limits visible to this process. A zero value is
// deliberately treated as unknown, rather than as unlimited.
type PreviewResources struct {
	CPUs        float64
	MemoryBytes int64
	CPUReason   string
	MemReason   string
}

type PreviewPolicy struct {
	BackgroundWorkers int
	RenderWorkers     int
	SourceReaders     int
	CPUBudget         int
	MemoryBytes       int64
	Schedule          string
	Reason            string
}

const fallbackPreviewMemory = 256 << 20

func choosePreviewPolicy(r PreviewResources, lookup func(string) (string, bool)) (PreviewPolicy, error) {
	knownCPU := r.CPUs > 0 && r.MemoryBytes > 0
	cpus := int(math.Floor(r.CPUs))
	if cpus < 1 {
		cpus = 1
	}
	background := 1
	render := 1
	readers := 1
	memory := int64(fallbackPreviewMemory)
	if knownCPU {
		// All photo work shares one hard concurrency budget equal to half the
		// effective cgroup/affinity allocation. On a one-CPU allocation we allow
		// one throttled job so preparation can still make progress.
		budget := max(1, cpus/2)
		render = budget
		background = max(1, min(8, budget/2))
		readers = max(1, min(4, budget))
	}
	schedule := "balanced"
	if value, ok := lookup("WEAZLCLOUD_PHOTO_SCHEDULE"); ok {
		schedule = strings.ToLower(strings.TrimSpace(value))
	}
	switch schedule {
	case "quiet":
		background = 1
	case "balanced":
	case "fast":
		background = max(1, min(16, cpus/2-1))
	default:
		return PreviewPolicy{}, fmt.Errorf("WEAZLCLOUD_PHOTO_SCHEDULE must be quiet, balanced, or fast")
	}
	if r.MemoryBytes > 0 {
		memory = min(int64(8<<30), r.MemoryBytes/8)
	}
	p := PreviewPolicy{BackgroundWorkers: background, RenderWorkers: render, SourceReaders: readers, CPUBudget: max(1, cpus/2), MemoryBytes: memory, Schedule: schedule, Reason: r.CPUReason + "; " + r.MemReason + "; 50% photo CPU ceiling"}
	for name, target := range map[string]*int{
		"WEAZLCLOUD_PREVIEW_BACKGROUND_WORKERS": &p.BackgroundWorkers,
		"WEAZLCLOUD_PREVIEW_TOTAL_WORKERS":      &p.RenderWorkers,
		"WEAZLCLOUD_PREVIEW_SOURCE_READERS":     &p.SourceReaders,
	} {
		if raw, ok := lookup(name); ok {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				return PreviewPolicy{}, fmt.Errorf("%s must be a positive integer", name)
			}
			*target = n
		}
	}
	if raw, ok := lookup("WEAZLCLOUD_PREVIEW_MEMORY_BYTES"); ok {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 || n > memory {
			return PreviewPolicy{}, fmt.Errorf("WEAZLCLOUD_PREVIEW_MEMORY_BYTES must be between 1 and %d", memory)
		}
		p.MemoryBytes = n
	}
	photoBudget := max(1, cpus/2)
	if p.RenderWorkers > photoBudget {
		p.RenderWorkers = photoBudget
	}
	p.CPUBudget = photoBudget
	if p.SourceReaders > p.RenderWorkers {
		p.SourceReaders = p.RenderWorkers
	}
	p.BackgroundWorkers = min(p.BackgroundWorkers, max(1, p.RenderWorkers-1))
	return p, nil
}

func defaultPreviewPolicy() PreviewPolicy {
	p, err := choosePreviewPolicy(discoverPreviewResources(), os.LookupEnv)
	if err != nil {
		return PreviewPolicy{BackgroundWorkers: 1, RenderWorkers: 1, SourceReaders: 1, CPUBudget: 1, MemoryBytes: fallbackPreviewMemory, Reason: "invalid override: " + err.Error()}
	}
	return p
}

// ValidatePreviewSettings is called before opening service listeners.
func ValidatePreviewSettings() error {
	if err := validatePreviewRenderer(); err != nil {
		return err
	}
	if err := validatePhotoWorkerSocket(); err != nil {
		return err
	}
	_, err := choosePreviewPolicy(discoverPreviewResources(), os.LookupEnv)
	return err
}

func readCPUQuota(root string) (float64, bool) {
	for _, path := range []string{filepath.Join(root, "cpu.max"), filepath.Join(root, "cpu.cfs_quota_us")} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parts := strings.Fields(string(b))
		if filepath.Base(path) == "cpu.cfs_quota_us" {
			period, _ := os.ReadFile(filepath.Join(root, "cpu.cfs_period_us"))
			parts = append(parts, strings.TrimSpace(string(period)))
		}
		if len(parts) < 2 || parts[0] == "max" {
			continue
		}
		quota, e1 := strconv.ParseFloat(parts[0], 64)
		period, e2 := strconv.ParseFloat(parts[1], 64)
		if e1 == nil && e2 == nil && quota > 0 && period > 0 {
			return quota / period, true
		}
	}
	return 0, false
}

func readCPUSet(root string) (float64, bool) {
	for _, path := range []string{filepath.Join(root, "cpuset.cpus.effective"), filepath.Join(root, "cpuset/cpuset.cpus")} {
		b, err := os.ReadFile(path)
		if err == nil {
			if n := countCPUSet(strings.TrimSpace(string(b))); n > 0 {
				return float64(n), true
			}
		}
	}
	return 0, false
}

func countCPUSet(value string) int {
	total := 0
	for _, part := range strings.Split(value, ",") {
		bounds := strings.SplitN(strings.TrimSpace(part), "-", 2)
		start, err := strconv.Atoi(bounds[0])
		if err != nil {
			continue
		}
		end := start
		if len(bounds) == 2 {
			end, err = strconv.Atoi(bounds[1])
			if err != nil || end < start {
				continue
			}
		}
		total += end - start + 1
	}
	return total
}

func readMemoryLimit(root string) (int64, bool) {
	for _, path := range []string{filepath.Join(root, "memory.max"), filepath.Join(root, "memory/memory.limit_in_bytes")} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(b))
		if value == "max" {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err == nil && n > 0 && n < (1<<62) {
			return n, true
		}
	}
	return 0, false
}
