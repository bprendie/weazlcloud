package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

const (
	maxInput  = 64 << 20
	maxOutput = 8 << 20
)

func main() {
	ready := flag.Bool("ready", false, "check whether the private worker socket accepts connections")
	probe := flag.Bool("probe", false, "render a small in-memory image through the private worker socket")
	flag.Parse()
	socket := strings.TrimSpace(os.Getenv("WEAZLCLOUD_PHOTO_WORKER_SOCKET"))
	if socket == "" {
		fmt.Fprintln(os.Stderr, "photo worker socket is not configured")
		os.Exit(2)
	}
	if *ready {
		conn, err := net.DialTimeout("unix", socket, time.Second)
		if err != nil {
			os.Exit(1)
		}
		_ = conn.Close()
		return
	}
	if *probe {
		if err := probeWorker(socket); err != nil {
			fmt.Fprintln(os.Stderr, "photo worker render probe failed")
			os.Exit(1)
		}
		return
	}
	if err := library.ValidatePhotoRenderer(); err != nil {
		fmt.Fprintln(os.Stderr, "photo renderer configuration is invalid")
		os.Exit(1)
	}
	if err := serve(socket); err != nil {
		fmt.Fprintln(os.Stderr, "photo worker stopped")
		os.Exit(1)
	}
}

func probeWorker(socket string) error {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 20, 12))); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, mime, err := previewrpc.Render(ctx, socket, source.Bytes(), 96, "raster")
	if err != nil || mime != "image/png" {
		return errors.New("worker returned an invalid response")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || format != "png" || config.Width != 96 || config.Height != 57 {
		return errors.New("worker returned an invalid derivative")
	}
	return nil
}

func serve(socket string) error {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close(); _ = os.Remove(socket) }()
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	server := &http.Server{Handler: newHandler(), ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 4096}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func newHandler() http.Handler {
	slots := make(chan struct{}, library.PhotoWorkerSlots())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-r.Context().Done():
			return
		}
		render(w, r)
	})
}

func render(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/thumbnail" {
		http.NotFound(w, r)
		return
	}
	if r.ContentLength <= 0 || r.ContentLength > maxInput {
		http.Error(w, "invalid source size", http.StatusRequestEntityTooLarge)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 2 || len(query["size"]) != 1 || len(query["media"]) != 1 {
		http.Error(w, "invalid preview request", http.StatusBadRequest)
		return
	}
	size, err := strconv.Atoi(query.Get("size"))
	media := query.Get("media")
	if err != nil || size < 96 || size > 1280 || (media != "raster" && media != "heif" && media != "video" && media != "native") {
		http.Error(w, "invalid preview request", http.StatusBadRequest)
		return
	}
	source, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInput))
	if err != nil || int64(len(source)) != r.ContentLength {
		clear(source)
		http.Error(w, "invalid source", http.StatusBadRequest)
		return
	}
	defer clear(source)
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	body, contentType, err := library.RenderPhotoPreview(ctx, source, size, media)
	if err != nil {
		http.Error(w, "preview unavailable", http.StatusUnprocessableEntity)
		return
	}
	if len(body) == 0 || len(body) > maxOutput {
		http.Error(w, "preview unavailable", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
	clear(body)
}
