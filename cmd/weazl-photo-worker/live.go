package main

import (
	"encoding/json"
	"github.com/bprendie/weazlcloud/internal/library"
	"io"
	"net/http"
)

func renderLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.ContentLength <= 0 || r.ContentLength > maxInput {
		http.Error(w, "invalid source", 413)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxInput))
	if err != nil || int64(len(data)) != r.ContentLength {
		clear(data)
		http.Error(w, "invalid source", 400)
		return
	}
	defer clear(data)
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/v1/live-identity" {
		media := r.URL.Query().Get("media")
		if media != "raster" && media != "heif" && media != "video" {
			http.Error(w, "invalid media", 400)
			return
		}
		identity, err := library.InspectLivePhoto(r.Context(), data, media)
		if err != nil {
			workerError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(identity)
		return
	}
	body, err := library.RenderLiveMotion(r.Context(), data)
	if err != nil {
		workerError(w, err)
		return
	}
	defer clear(body)
	w.Header().Set("Content-Type", "video/mp4")
	w.Write(body)
}
