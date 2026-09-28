package library

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func discoverPreviewResources() PreviewResources {
	return discoverResources(os.ReadFile, runtime.NumCPU(), runtime.GOMAXPROCS(0))
}

// read is injected so nested v1/v2 mounts can be tested without host changes.
func discoverResources(read func(string) ([]byte, error), cpus, procs int) PreviewResources {
	r := PreviewResources{CPUs: float64(min(cpus, procs)), CPUReason: "affinity/execution budget", MemReason: "host/cgroup minimum"}
	text := func(path string) string { b, _ := read(path); return strings.TrimSpace(string(b)) }
	for _, line := range strings.Split(text("/proc/self/status"), "\n") {
		if value, ok := strings.CutPrefix(line, "Cpus_allowed_list:"); ok {
			if n := countCPUSet(strings.TrimSpace(value)); n > 0 {
				r.CPUs = min(r.CPUs, float64(n))
			}
		}
	}
	for _, line := range strings.Split(text("/proc/meminfo"), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			if n > 0 {
				r.MemoryBytes = n * 1024
			}
		}
	}
	groups := map[string]string{}
	resolved := false
	for _, line := range strings.Split(text("/proc/self/cgroup"), "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) != 3 {
			continue
		}
		for _, controller := range strings.Split(f[1], ",") {
			groups[controller] = f[2]
		}
	}
	for _, line := range strings.Split(text("/proc/self/mountinfo"), "\n") {
		before, after, ok := strings.Cut(line, " - ")
		a, b := strings.Fields(before), strings.Fields(after)
		if !ok || len(a) < 5 || len(b) < 3 || (b[0] != "cgroup" && b[0] != "cgroup2") {
			continue
		}
		controllers := strings.Split(b[2], ",")
		if b[0] == "cgroup2" {
			controllers = []string{""}
		}
		for _, controller := range controllers {
			group, exists := groups[controller]
			if !exists {
				continue
			}
			root, mount := unescapeMount(a[3]), unescapeMount(a[4])
			rel, err := filepath.Rel(root, group)
			if err != nil {
				continue
			}
			// A cgroup namespace may report / while the mount retains its host root.
			if group == "/" {
				rel = "."
			}
			if rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			resolved = true
			for dir := filepath.Join(mount, rel); ; dir = filepath.Dir(dir) {
				applyCgroupLimits(&r, dir, b[0] == "cgroup2", text)
				if dir == mount || filepath.Dir(dir) == dir {
					break
				}
			}
		}
	}
	if !resolved || r.CPUs <= 0 || r.MemoryBytes <= 0 {
		r.CPUs = 0
		if r.MemoryBytes > 0 {
			r.MemoryBytes = min(r.MemoryBytes, 8*fallbackPreviewMemory)
		}
		r.CPUReason, r.MemReason = "resource discovery incomplete", "fallback 256 MiB"
	}
	return r
}

func applyCgroupLimits(r *PreviewResources, dir string, v2 bool, text func(string) string) {
	get := func(name string) string { return text(filepath.Join(dir, name)) }
	quota, period, cpuset, memory := "", "", "", ""
	if v2 {
		f := strings.Fields(get("cpu.max"))
		if len(f) > 0 && len(f) != 2 {
			r.CPUs = min(r.CPUs, 1)
		}
		if len(f) == 2 {
			quota, period = f[0], f[1]
		}
		cpuset, memory = get("cpuset.cpus.effective"), get("memory.max")
	} else {
		quota, period = get("cpu.cfs_quota_us"), get("cpu.cfs_period_us")
		cpuset, memory = get("cpuset.cpus"), get("memory.limit_in_bytes")
	}
	q, e1 := strconv.ParseFloat(quota, 64)
	p, e2 := strconv.ParseFloat(period, 64)
	if e1 == nil && e2 == nil && q > 0 && p > 0 {
		r.CPUs = min(r.CPUs, q/p)
	} else if quota != "" && quota != "max" && quota != "-1" {
		r.CPUs = min(r.CPUs, 1)
	}
	if n := countCPUSet(cpuset); n > 0 {
		r.CPUs = min(r.CPUs, float64(n))
	}
	if n, err := strconv.ParseInt(memory, 10, 64); err == nil && n > 0 && n < 1<<60 {
		if r.MemoryBytes == 0 || n < r.MemoryBytes {
			r.MemoryBytes = n
		}
	} else if memory != "" && memory != "max" && (err != nil || n <= 0) {
		r.MemoryBytes = min(r.MemoryBytes, 8*fallbackPreviewMemory)
		r.CPUs = min(r.CPUs, 1)
	}
}

func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}
