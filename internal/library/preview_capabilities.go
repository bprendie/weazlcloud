package library

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

var previewCapabilities = struct {
	sync.Mutex
	paths map[string]bool
}{paths: map[string]bool{}}

func supportsPreviewBundle(ctx context.Context, path string) bool {
	previewCapabilities.Lock()
	defer previewCapabilities.Unlock()
	if value, ok := previewCapabilities.paths[path]; ok {
		return value
	}
	work, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(work, path, "--capabilities")
	out := &previewOutput{limit: 128}
	cmd.Stdout = out
	err := cmd.Run()
	value := err == nil && string(out.Bytes()) == "bundle-v1\n"
	if work.Err() == nil {
		if len(previewCapabilities.paths) >= 32 {
			clear(previewCapabilities.paths)
		}
		previewCapabilities.paths[path] = value
	}
	return value
}
