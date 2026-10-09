package main

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/previewrpc"
)

func renderBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.ContentLength <= 0 || r.ContentLength > int64(previewrpc.InputLimit(r.URL.Query().Get("media"))) {
		http.Error(w, "invalid source", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	media := q.Get("media")
	if len(q) != 2 || len(q["sizes"]) != 1 || len(q["media"]) != 1 || (media != "raster" && media != "heif" && media != "video" && media != "native") {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var sizes []int
	for _, value := range strings.Split(q.Get("sizes"), ",") {
		size, err := strconv.Atoi(value)
		if err != nil {
			http.Error(w, "invalid size", 400)
			return
		}
		sizes = append(sizes, size)
	}
	if !previewrpc.BundleSizes(sizes) {
		http.Error(w, "invalid sizes", 400)
		return
	}
	source, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(previewrpc.InputLimit(media))))
	if err != nil || int64(len(source)) != r.ContentLength {
		clear(source)
		http.Error(w, "invalid source", 400)
		return
	}
	defer clear(source)
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/vnd.weazl.preview-bundle")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	var hashSent bool
	total := 0
	err = library.RenderPhotoBundle(ctx, source, sizes, media, func(v previewrpc.Variant) error {
		total += len(v.Body) + 7
		if total > previewrpc.MaxBundleOutput-1024 {
			return previewrpc.ErrTooLarge
		}
		if !hashSent && len(v.ThumbHash) > 0 {
			if err := previewrpc.WriteHash(w, v.ThumbHash); err != nil {
				return err
			}
			hashSent = true
		}
		err := previewrpc.WriteVariant(w, v)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return err
	})
	if r.URL.Path == "/v3/thumbnails" && err != nil {
		_ = previewrpc.EndBundleError(w, err)
	} else {
		_ = previewrpc.EndBundle(w, err != nil)
	}
}
