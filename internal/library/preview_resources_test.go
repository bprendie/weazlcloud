package library

import "testing"

func envMap(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestPreviewPolicyProfiles(t *testing.T) {
	tests := []struct {
		name                string
		cpu                 float64
		mem                 int64
		bg, render, readers int
		budget              int64
	}{
		{"small", 2, 4 << 30, 1, 2, 1, 512 << 20},
		{"medium", 4, 8 << 30, 2, 3, 2, 1 << 30},
		{"large", 32, 128 << 30, 8, 10, 4, 8 << 30},
		{"one cpu", 1, 1 << 30, 1, 1, 1, 128 << 20},
		{"unknown", 0, 0, 1, 1, 1, 256 << 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := choosePreviewPolicy(PreviewResources{CPUs: tc.cpu, MemoryBytes: tc.mem, CPUReason: "test", MemReason: "test"}, envMap(nil))
			if err != nil || p.BackgroundWorkers != tc.bg || p.RenderWorkers != tc.render || p.SourceReaders != tc.readers || p.MemoryBytes != tc.budget {
				t.Fatalf("policy=%+v err=%v", p, err)
			}
		})
	}
}

func TestPreviewPolicyHonorsOverridesAndBounds(t *testing.T) {
	p, err := choosePreviewPolicy(PreviewResources{CPUs: 4, MemoryBytes: 8 << 30}, envMap(map[string]string{
		"WEAZLCLOUD_PREVIEW_BACKGROUND_WORKERS": "8",
		"WEAZLCLOUD_PREVIEW_TOTAL_WORKERS":      "3",
		"WEAZLCLOUD_PREVIEW_SOURCE_READERS":     "4",
		"WEAZLCLOUD_PREVIEW_MEMORY_BYTES":       "536870912",
	}))
	if err != nil || p.BackgroundWorkers != 2 || p.RenderWorkers != 3 || p.SourceReaders != 3 || p.MemoryBytes != 536870912 {
		t.Fatalf("override policy=%+v err=%v", p, err)
	}
	if _, err := choosePreviewPolicy(PreviewResources{CPUs: 4}, envMap(map[string]string{"WEAZLCLOUD_PREVIEW_TOTAL_WORKERS": "nope"})); err == nil {
		t.Fatal("invalid worker override accepted")
	}
}

func TestPreviewResourceParsers(t *testing.T) {
	if got := countCPUSet("0-3,6,8-9"); got != 7 {
		t.Fatalf("cpuset count=%d", got)
	}
	if got, ok := readCPUQuota(t.TempDir()); ok || got != 0 {
		t.Fatalf("missing cpu quota=%v,%v", got, ok)
	}
}
