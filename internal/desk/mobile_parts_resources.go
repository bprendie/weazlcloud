package desk

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Inspect this process's v1/v2 hierarchy, including ancestor limits and cgroup
// namespaces. GOMAXPROCS supplies the execution ceiling; quotas may lower it.
func mobileFinalizeResources(read func(string) ([]byte, error), cpus int) (int, int64) {
	text := func(path string) string { b, _ := read(path); return strings.TrimSpace(string(b)) }
	var memory int64
	for _, line := range strings.Split(text("/proc/meminfo"), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			n, err := strconv.ParseInt(f[1], 10, 64)
			if err == nil && n > 0 && n < (1<<53) {
				memory = n * 1024
			}
		}
	}
	groups := map[string]string{}
	for _, line := range strings.Split(text("/proc/self/cgroup"), "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) == 3 {
			for _, c := range strings.Split(f[1], ",") {
				groups[c] = f[2]
			}
		}
	}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	resolved := false
	for _, line := range strings.Split(text("/proc/self/mountinfo"), "\n") {
		before, after, ok := strings.Cut(line, " - ")
		a, b := strings.Fields(before), strings.Fields(after)
		if !ok || len(a) < 5 || len(b) < 3 || (b[0] != "cgroup" && b[0] != "cgroup2") {
			continue
		}
		controllers := strings.Split(b[2], ",")
		v2 := b[0] == "cgroup2"
		if v2 {
			controllers = []string{""}
		}
		for _, controller := range controllers {
			group, exists := groups[controller]
			if !exists || (!v2 && controller != "memory" && controller != "cpu") {
				continue
			}
			root, mount := unescape.Replace(a[3]), unescape.Replace(a[4])
			rel, err := filepath.Rel(root, group)
			if group == "/" {
				rel = "."
			}
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			if v2 || controller == "memory" {
				resolved = true
			}
			for dir := filepath.Join(mount, rel); ; dir = filepath.Dir(dir) {
				memName := "memory.limit_in_bytes"
				if v2 {
					memName = "memory.max"
				}
				raw := text(filepath.Join(dir, memName))
				if n, e := strconv.ParseInt(raw, 10, 64); e == nil && n > 0 {
					if memory == 0 || n < memory {
						memory = n
					}
				} else if raw != "" && raw != "max" {
					memory = 0
					cpus = 1
				}
				quota, period := text(filepath.Join(dir, "cpu.cfs_quota_us")), text(filepath.Join(dir, "cpu.cfs_period_us"))
				if v2 {
					f := strings.Fields(text(filepath.Join(dir, "cpu.max")))
					if len(f) == 2 {
						quota, period = f[0], f[1]
					}
				}
				q, e1 := strconv.ParseInt(quota, 10, 64)
				p, e2 := strconv.ParseInt(period, 10, 64)
				if e1 == nil && e2 == nil && q > 0 && p > 0 {
					cpus = min(cpus, max(1, int(q/p)))
				}
				if dir == mount || filepath.Dir(dir) == dir {
					break
				}
			}
		}
	}
	if !resolved {
		return 1, memory
	}
	return cpus, memory
}
