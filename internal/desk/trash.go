package desk

import (
	"net/http"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type trashRequest struct {
	Path string `json:"path"`
}

func trashViews(items []catalog.File) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		view := map[string]any{
			"path": item.Path, "folder": item.Folder, "size": item.Size, "mtime": item.Mtime,
		}
		if item.DeletedAt != nil {
			view["deleted_at"] = item.DeletedAt
		}
		out = append(out, view)
	}
	return out
}

func (h *Handler) trash(w http.ResponseWriter, r *http.Request) {
	if h.lib == nil || h.vault == nil || !h.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	items, err := h.lib.Trash(r.Context())
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": trashViews(items), "retention_days": int(library.TrashLifetime / (24 * time.Hour))})
}

func (h *Handler) restoreTrash(w http.ResponseWriter, r *http.Request) {
	if h.lib == nil || h.vault == nil || !h.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body trashRequest
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	if err := h.lib.Restore(r.Context(), body.Path); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "restored"})
}

func (h *Handler) cleanupTrash(w http.ResponseWriter, r *http.Request) {
	if h.lib == nil || h.vault == nil || !h.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	bytes, err := h.lib.CleanupTrash(r.Context(), time.Now().UTC().Add(-library.TrashLifetime))
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reclaimed_bytes": bytes})
}

func (h *Handler) multiTrash(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	items, err := res.Lib.Trash(r.Context())
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": trashViews(items), "retention_days": int(library.TrashLifetime / (24 * time.Hour))})
}

func (h *Handler) multiRestoreTrash(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body trashRequest
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	if err := res.Lib.Restore(r.Context(), body.Path); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "restored"})
}

func (h *Handler) multiCleanupTrash(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	bytes, err := res.Lib.CleanupTrash(r.Context(), time.Now().UTC().Add(-library.TrashLifetime))
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reclaimed_bytes": bytes})
}
