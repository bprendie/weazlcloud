package desk

import (
	"net/http"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/upload"
)

func (h *Handler) photoCapabilities(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	roots := []library.PhotoUploadRoot{}
	if res.Vault.Unlocked() {
		roots, err = res.Lib.PhotoUploadRoots(r.Context())
		if err != nil {
			photoAPIError(w, err)
			return
		}
	}
	writeJSON(w, 200, map[string]any{
		"version": 1, "vault_unlocked": res.Vault.Unlocked(), "page_limit": library.PhotoPageMaximum,
		"chunk_limit": upload.MaxChunkBytes, "upload_components": []string{"original", "motion"},
		"roots":              roots,
		"device_credentials": true, "durable_sync": true, "hidden_context": true, "album_edits": true,
		"gallery_grabs": true, "live_photo_uploads": true, "backup_direction": "device_to_vault",
		"locked_ingestion": "rejected", "checkpoint_expiry": "8192 catalog changes",
	})
}
