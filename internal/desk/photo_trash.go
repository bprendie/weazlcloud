package desk

import (
	"net/http"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
)

func (h *Handler) multiPhotoTrash(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	items, err := res.Lib.PhotoTrash(r.Context(), r.URL.Query().Get("hidden") == "1")
	if err != nil {
		apiError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": trashViews(items), "retention_days": int(library.TrashLifetime / (24 * time.Hour))})
}

func (h *Handler) multiRestorePhotoTrash(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body, 512) {
		return
	}
	if err := res.Lib.RestorePhoto(r.Context(), body.ID, r.URL.Query().Get("hidden") == "1"); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "restored"})
}
