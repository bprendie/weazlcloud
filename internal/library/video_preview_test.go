package library

import (
	"bytes"
	"context"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

func TestQuickTimePortraitPosters(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "go")
	for _, codec := range []string{"libx264", "libx265"} {
		t.Run(codec, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "portrait.MOV")
			args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc2=size=180x320:rate=15:duration=2", "-c:v", codec, "-threads", "1", "-preset", "ultrafast"}
			if codec == "libx265" {
				args = append(args, "-x265-params", "pools=none:frame-threads=1:log-level=error", "-tag:v", "hvc1")
			}
			if out, err := exec.Command(ffmpeg, append(args, path)...).CombinedOutput(); err != nil {
				t.Fatalf("create QuickTime fixture: %v: %s", err, out)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Index(data, []byte("moov")) < bytes.Index(data, []byte("mdat")) {
				t.Fatal("fixture must require seeking from the trailing movie index")
			}
			body, mime, err := RenderPhotoPreview(context.Background(), data, 320, "video")
			cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(body))
			if err != nil || decodeErr != nil || mime != "image/jpeg" || cfg.Width >= cfg.Height || cfg.Height != 320 {
				t.Fatalf("portrait poster: %+v %s %v %v", cfg, mime, err, decodeErr)
			}
			count := 0
			err = RenderPhotoBundle(context.Background(), data, []int{96, 320}, "video", func(v previewrpc.Variant) error {
				count++
				cfg, _, err := image.DecodeConfig(bytes.NewReader(v.Body))
				if err != nil || cfg.Height != v.Size || cfg.Width >= cfg.Height || len(v.ThumbHash) == 0 {
					t.Fatalf("portrait bundle: %+v %v", cfg, err)
				}
				return nil
			})
			if err != nil || count != 2 {
				t.Fatal("bundle failed", count, err)
			}
			// Both public Library thumbnails and the Photos timeline use this path.
			l := newPhotoIndexTestLibrary(t)
			file, err := l.Put(context.Background(), "Photos/portrait.MOV", data)
			if err != nil {
				t.Fatal(err)
			}
			file, err = l.Metadata(context.Background(), file.Path)
			if err != nil {
				t.Fatal(err)
			}
			for index, bundle := range []string{"on", "off"} {
				t.Setenv("WEAZLCLOUD_PREVIEW_BUNDLE", bundle)
				if _, _, err := l.Thumbnail(context.Background(), file.Path, 160+index); err != nil {
					t.Fatal("Library MOV", err)
				}
				if _, _, err := l.PhotoThumbnailVisible(context.Background(), file.EntryID, 96+index, false); err != nil {
					t.Fatal("Photos MOV", err)
				}
			}
		})
	}
}

func TestVideoPosterRejectsInvalidInputAndCancellation(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	for _, input := range []string{"broken mov", "#EXTM3U\nfile:///etc/passwd\n"} {
		if _, _, err := RenderPhotoPreview(context.Background(), []byte(input), 96, "video"); err == nil {
			t.Fatal("invalid video accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := RenderPhotoPreview(ctx, []byte("video"), 96, "video"); err == nil {
		t.Fatal("canceled preview succeeded")
	}
}
