package main

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

func TestPrivateWorkerRendersOverUnixSocket(t *testing.T) {
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "go")
	socket := filepath.Join(t.TempDir(), "worker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: newHandler()}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-done
	})

	var input bytes.Buffer
	if err := png.Encode(&input, image.NewRGBA(image.Rect(0, 0, 20, 12))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	body, contentType, err := previewrpc.Render(ctx, socket, input.Bytes(), 96, "raster")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/png" {
		t.Fatalf("content type=%q", contentType)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "png" || config.Width != 96 || config.Height != 57 {
		t.Fatalf("preview config=%+v format=%q err=%v", config, format, err)
	}
}

func TestFFmpegVideoPosterFromPipe(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "go")
	command := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=blue:s=32x24:r=1:d=1", "-frames:v", "1", "-c:v", "mpeg4", "-f", "matroska", "pipe:1")
	video, err := command.Output()
	if err != nil {
		t.Fatalf("create test video: %v", err)
	}
	body, mime, err := library.RenderPhotoPreview(context.Background(), video, 96, "video")
	if err != nil || mime != "image/jpeg" {
		t.Fatalf("poster mime=%q bytes=%d err=%v", mime, len(body), err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "jpeg" || config.Width > 96 || config.Height > 96 {
		t.Fatalf("poster config=%+v format=%q err=%v", config, format, err)
	}
}

func TestFFmpegHEIFPreviewFromPipe(t *testing.T) {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	heifEncoder, err := exec.LookPath("heif-enc")
	if err != nil {
		t.Skip("heif-enc not installed")
	}
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "go")
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.png")
	heifPath := filepath.Join(dir, "source.heic")
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 20, 12))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, source.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(heifEncoder, "--rotate-cw", "90", "--color-profile", "709", inputPath, "-o", heifPath).CombinedOutput(); err != nil {
		t.Fatalf("create test HEIF: %v (%s)", err, output)
	}
	encoded, err := os.ReadFile(heifPath)
	if err != nil {
		t.Fatal(err)
	}
	body, mime, err := library.RenderPhotoPreview(context.Background(), encoded, 96, "heif")
	if err != nil || mime != "image/jpeg" {
		t.Fatalf("HEIF preview mime=%q bytes=%d err=%v", mime, len(body), err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "jpeg" || config.Width != 58 || config.Height != 96 {
		t.Fatalf("rotated HEIF preview config=%+v format=%q err=%v", config, format, err)
	}
}
