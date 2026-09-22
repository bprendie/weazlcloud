package desk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) libraryEvents(w http.ResponseWriter, r *http.Request) {
	if h.vault == nil || h.lib == nil || h.changes == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	if !h.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h.serveLibraryEvents(w, r, h.changes)
}

func (h *Handler) multiLibraryEvents(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h.serveLibraryEvents(w, r, res.Changes)
}

func (h *Handler) serveLibraryEvents(w http.ResponseWriter, r *http.Request, hub *filesvc.Hub) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming unavailable"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	stream, cancel := hub.Subscribe()
	defer cancel()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case event, ok := <-stream:
			if !ok {
				return
			}
			body, err := json.Marshal(event.Change)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: library\ndata: %s\n\n", event.Version, body)
			flusher.Flush()
		}
	}
}
