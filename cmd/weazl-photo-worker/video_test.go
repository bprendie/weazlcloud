package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

func TestWorkerQuickTimeSingleAndBundle(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "go")
	path := filepath.Join(t.TempDir(), "portrait.mov")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=180x320:rate=15:duration=2", "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", path).CombinedOutput(); err != nil {
		t.Fatalf("MOV fixture: %v %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A trailing QuickTime free atom takes this valid movie over the old 64-MiB
	// bound without a huge synthetic video encode or changing media offsets.
	padding := make([]byte, 65<<20)
	binary.BigEndian.PutUint32(padding[:4], uint32(len(padding)))
	copy(padding[4:8], "free")
	data = append(data, padding...)
	for _, path := range []string{"/v1/thumbnail?size=320&media=video", "/v3/thumbnails?sizes=320,1280&media=video"} {
		out := httptest.NewRecorder()
		render(out, httptest.NewRequest("POST", path, bytes.NewReader(data)))
		if out.Code != 200 || out.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("worker: %d %s", out.Code, out.Body.String())
		}
		if out.Header().Get("Content-Type") == "image/jpeg" {
			cfg, _, err := image.DecodeConfig(out.Body)
			if err != nil || cfg.Width >= cfg.Height || cfg.Height != 320 {
				t.Fatal("single poster", cfg, err)
			}
			continue
		}
		count := 0
		err := previewrpc.ReadBundle(out.Body, []int{320, 1280}, func(v previewrpc.Variant) error {
			count++
			cfg, _, err := image.DecodeConfig(bytes.NewReader(v.Body))
			if err != nil || cfg.Width >= cfg.Height || cfg.Height != v.Size {
				t.Fatal("bundle poster", cfg, err)
			}
			return nil
		})
		if err != nil || count != 2 {
			t.Fatal("incomplete bundle", count, err)
		}
	}
}

func TestWorkerMediaSpecificInputBounds(t *testing.T) {
	for _, endpoint := range []string{"/v1/thumbnail?size=320", "/v3/thumbnails?sizes=320,1280"} {
		for _, media := range []string{"raster", "heif", "video"} {
			req := httptest.NewRequest("POST", endpoint+"&media="+media, bytes.NewReader(nil))
			req.ContentLength = int64(previewrpc.InputLimit(media)) + 1
			out := httptest.NewRecorder()
			render(out, req)
			if out.Code < 400 {
				t.Fatalf("unbounded %s: %d", media, out.Code)
			}
		}
	}
}
