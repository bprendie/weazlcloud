package library

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

// Generated 32x24 blank image; no private media.
//
//go:embed testdata/worker-probe.heic
var workerProbeHEIC []byte

func PhotoWorkerProbeHEIC() []byte { return append([]byte(nil), workerProbeHEIC...) }

func ValidatePhotoWorker() error {
	dir, err := os.MkdirTemp("", "weazl-worker-check-")
	if err != nil {
		return fmt.Errorf("%w: scratch directory", previewrpc.ErrEnvironment)
	}
	defer os.RemoveAll(dir)
	if err := os.Symlink("/proc/self/fd/3", filepath.Join(dir, "source.heic")); err != nil {
		return fmt.Errorf("%w: scratch symlink", previewrpc.ErrEnvironment)
	}
	for _, tool := range []string{"heif-convert", "ffmpeg", "exiftool"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%w: missing %s", previewrpc.ErrEnvironment, tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, err := convertHEIFPipe(ctx, workerProbeHEIC)
	clear(body)
	if err != nil {
		return fmt.Errorf("%w: HEIC decoder", previewrpc.ErrEnvironment)
	}
	return ValidatePhotoRenderer()
}
