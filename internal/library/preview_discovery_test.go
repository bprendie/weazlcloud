package library

import (
	"os"
	"testing"
)

func resourceFixture(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		if s, ok := files[path]; ok {
			return []byte(s), nil
		}
		return nil, os.ErrNotExist
	}
}

func TestPreviewDiscoveryNestedV2AndAncestorLimits(t *testing.T) {
	files := map[string]string{
		"/proc/meminfo":                               "MemTotal: 134217728 kB",
		"/proc/self/status":                           "Cpus_allowed_list:\t0-31",
		"/proc/self/cgroup":                           "0::/tenant/app",
		"/proc/self/mountinfo":                        "1 2 0:3 / /sys/fs/cgroup rw - cgroup2 cgroup rw",
		"/sys/fs/cgroup/tenant/app/cpu.max":           "max 100000",
		"/sys/fs/cgroup/tenant/app/memory.max":        "8589934592",
		"/sys/fs/cgroup/tenant/cpu.max":               "150000 100000",
		"/sys/fs/cgroup/tenant/memory.max":            "4294967296",
		"/sys/fs/cgroup/tenant/cpuset.cpus.effective": "2-5",
	}
	r := discoverResources(resourceFixture(files), 32, 32)
	if r.CPUs != 1.5 || r.MemoryBytes != 4<<30 {
		t.Fatalf("limits: %+v", r)
	}
	files["/proc/self/cgroup"] = "0::/"
	files["/proc/self/mountinfo"] = "1 2 0:3 /tenant/app /sys/fs/cgroup rw - cgroup2 cgroup rw"
	files["/sys/fs/cgroup/cpu.max"] = "50000 100000"
	files["/sys/fs/cgroup/memory.max"] = "1073741824"
	r = discoverResources(resourceFixture(files), 32, 32)
	if r.CPUs != 0.5 || r.MemoryBytes != 1<<30 {
		t.Fatalf("namespace limits: %+v", r)
	}
}

func TestPreviewDiscoveryNestedV1SeparateControllers(t *testing.T) {
	files := map[string]string{
		"/proc/meminfo":                        "MemTotal: 134217728 kB",
		"/proc/self/cgroup":                    "2:cpu,cpuacct:/tenant/app\n3:memory:/tenant/app\n4:cpuset:/tenant/app",
		"/proc/self/mountinfo":                 "1 2 0:3 /tenant /cg/cpu rw - cgroup cgroup rw,cpu,cpuacct\n2 2 0:4 /tenant /cg/memory rw - cgroup cgroup rw,memory\n3 2 0:5 /tenant /cg/cpuset rw - cgroup cgroup rw,cpuset",
		"/cg/cpu/app/cpu.cfs_quota_us":         "200000",
		"/cg/cpu/app/cpu.cfs_period_us":        "100000",
		"/cg/memory/app/memory.limit_in_bytes": "8589934592",
		"/cg/memory/memory.limit_in_bytes":     "4294967296",
		"/cg/cpuset/app/cpuset.cpus":           "0-7",
	}
	r := discoverResources(resourceFixture(files), 32, 32)
	if r.CPUs != 2 || r.MemoryBytes != 4<<30 {
		t.Fatalf("limits: %+v", r)
	}
}

func TestPreviewPolicyRejectsMemoryAboveDetectedBudget(t *testing.T) {
	_, err := choosePreviewPolicy(PreviewResources{CPUs: 32, MemoryBytes: 4 << 30}, envMap(map[string]string{"WEAZLCLOUD_PREVIEW_MEMORY_BYTES": "8589934592"}))
	if err == nil {
		t.Fatal("unsafe memory override accepted")
	}
	r := discoverResources(resourceFixture(nil), 32, 32)
	p, err := choosePreviewPolicy(r, envMap(nil))
	if err != nil || p.RenderWorkers != 1 || p.MemoryBytes != fallbackPreviewMemory {
		t.Fatalf("fallback %+v: %v", p, err)
	}
}

func TestPreviewDiscoveryMalformedLimitsAndSmallHost(t *testing.T) {
	files := map[string]string{
		"/proc/meminfo":        "MemTotal: 262144 kB",
		"/proc/self/cgroup":    "0::/app",
		"/proc/self/mountinfo": "1 2 0:3 / /cg rw - cgroup2 cgroup rw",
		"/cg/app/cpu.max":      "broken",
		"/cg/app/memory.max":   "broken",
	}
	r := discoverResources(resourceFixture(files), 32, 32)
	p, err := choosePreviewPolicy(r, envMap(nil))
	if err != nil || p.RenderWorkers != 1 || p.MemoryBytes != 32<<20 {
		t.Fatalf("unsafe small-host policy: %+v %v", p, err)
	}
}
