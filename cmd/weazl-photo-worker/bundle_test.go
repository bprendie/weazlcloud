package main

import (
	"bytes"
	"context"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateWorkerBundleVariants(t *testing.T) {
	t.Setenv("WEAZLCLOUD_PREVIEW_RENDERER", "go")
	socket := filepath.Join(t.TempDir(), "bundle.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: newHandler()}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() { server.Close(); <-done }()
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	var jpegBytes, pngBytes, gifBytes bytes.Buffer
	_ = jpeg.Encode(&jpegBytes, img, nil)
	_ = png.Encode(&pngBytes, img)
	_ = gif.Encode(&gifBytes, image.NewPaletted(img.Bounds(), color.Palette{color.Black, color.White}), nil)
	for kind, source := range map[string][]byte{"jpeg": jpegBytes.Bytes(), "png": pngBytes.Bytes(), "gif": gifBytes.Bytes()} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			count := 0
			err := previewrpc.RenderBundle(ctx, socket, source, []int{320, 1280}, "raster", func(v previewrpc.Variant) error {
				count++
				cfg, _, err := image.DecodeConfig(bytes.NewReader(v.Body))
				if err != nil {
					return err
				}
				if cfg.Width != v.Size || len(v.ThumbHash) < 5 {
					t.Errorf("bad variant %+v hash=%d", cfg, len(v.ThumbHash))
				}
				return nil
			})
			if err != nil || count != 2 {
				t.Fatal(count, err)
			}
		})
	}
	if err := previewrpc.RenderBundle(context.Background(), socket, []byte("broken"), []int{320, 1280}, "raster", func(previewrpc.Variant) error { t.Fatal("corrupt output"); return nil }); err == nil {
		t.Fatal("corrupt source succeeded")
	}
}
