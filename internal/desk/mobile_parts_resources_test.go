package desk

import (
	"os"
	"testing"
)

func TestMobileFinalizeResourcesCgroups(t *testing.T) {
	for _, tc := range []struct {
		name, groups, mounts string
		files                map[string]string
		cpus                 int
		memory               int64
	}{
		{"nested-v2", "0::/tenant/app", "1 2 0:3 / /cg rw - cgroup2 cgroup rw", map[string]string{
			"/cg/tenant/app/memory.max": "8589934592", "/cg/tenant/memory.max": "2147483648",
			"/cg/tenant/app/cpu.max": "max 100000", "/cg/tenant/cpu.max": "150000 100000",
		}, 1, 2 << 30},
		{"namespaced-v2", "0::/", "1 2 0:3 /tenant/app /cg rw - cgroup2 cgroup rw", map[string]string{
			"/cg/memory.max": "1073741824", "/cg/cpu.max": "400000 100000",
		}, 4, 1 << 30},
		{"nested-v1", "2:cpu,cpuacct:/tenant/app\n3:memory:/tenant/app", "1 2 0:3 /tenant /cg/cpu rw - cgroup cgroup rw,cpu,cpuacct\n2 2 0:4 /tenant /cg/memory rw - cgroup cgroup rw,memory", map[string]string{
			"/cg/memory/app/memory.limit_in_bytes": "8589934592", "/cg/memory/memory.limit_in_bytes": "4294967296",
			"/cg/cpu/cpu.cfs_quota_us": "200000", "/cg/cpu/cpu.cfs_period_us": "100000",
		}, 2, 4 << 30},
		{"unlimited-v2", "0::/", "1 2 0:3 / /cg rw - cgroup2 cgroup rw", map[string]string{"/cg/memory.max": "max"}, 16, 64 << 30},
		{"unknown", "", "", nil, 1, 64 << 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"/proc/meminfo": "MemTotal: 67108864 kB", "/proc/self/cgroup": tc.groups, "/proc/self/mountinfo": tc.mounts}
			for k, v := range tc.files {
				files[k] = v
			}
			cpus, memory := mobileFinalizeResources(func(path string) ([]byte, error) { return []byte(files[path]), nil }, 16)
			if cpus != tc.cpus || memory != tc.memory {
				t.Fatalf("got %d CPUs / %d RAM; want %d / %d", cpus, memory, tc.cpus, tc.memory)
			}
		})
	}
}

func TestMobileFinalizerMemoryLimits(t *testing.T) {
	for _, tc := range []struct {
		memory int64
		want   mobileFinalizeLimits
	}{
		{0, mobileFinalizeLimits{1, 1}}, {512 << 20, mobileFinalizeLimits{1, 1}},
		{2 << 30, mobileFinalizeLimits{1, 1}}, {4 << 30, mobileFinalizeLimits{2, 2}},
		{8 << 30, mobileFinalizeLimits{4, 4}}, {16 << 30, mobileFinalizeLimits{8, 8}},
	} {
		got := resolveMobileFinalizerLimits(32, tc.memory, func(string) string { return "" })
		if got != tc.want {
			t.Errorf("memory=%d: got %+v want %+v", tc.memory, got, tc.want)
		}
	}
}

func TestMobileFinalizerSettingsSnapshotAndValidation(t *testing.T) {
	t.Setenv("WEAZLCLOUD_MOBILE_FINALIZE_WORKERS", "3")
	t.Setenv("WEAZLCLOUD_MOBILE_FINALIZE_WORKERS_PER_OWNER", "2")
	if err := ValidateMobileFinalizeSettings(); err != nil {
		t.Fatal(err)
	}
	h := &Handler{}
	first := h.mobileFinalizerLimits()
	t.Setenv("WEAZLCLOUD_MOBILE_FINALIZE_WORKERS", "8")
	if got := h.mobileFinalizerLimits(); got != first || got != (mobileFinalizeLimits{3, 2}) {
		t.Fatalf("settings changed: %+v", got)
	}
	for _, name := range []string{"WEAZLCLOUD_MOBILE_FINALIZE_WORKERS", "WEAZLCLOUD_MOBILE_FINALIZE_WORKERS_PER_OWNER"} {
		before := os.Getenv(name)
		for _, bad := range []string{"0", "-1", "9", "garbage", "999999999999999999999"} {
			t.Setenv(name, bad)
			if err := ValidateMobileFinalizeSettings(); err == nil {
				t.Errorf("accepted %s=%s", name, bad)
			}
		}
		t.Setenv(name, before)
	}
}
