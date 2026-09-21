package desk

import (
	"net/http"
	"strconv"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) thumbnailLibrary(w http.ResponseWriter, r *http.Request) {
	h.thumbnailLibraryFor(w, r, h.vault, h.lib)
}

func (h *Handler) thumbnailLibraryFor(w http.ResponseWriter, r *http.Request, v *vault.Vault, l *library.Library) {
	if l == nil || v == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	if !v.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	size := 320
	if raw := r.URL.Query().Get("size"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			size = parsed
		}
	}
	body, contentType, err := l.Thumbnail(r.Context(), r.URL.Query().Get("path"), size)
	if err != nil {
		if err == library.ErrThumbnailUnavailable {
			apiError(w, err)
			return
		}
		apiError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
