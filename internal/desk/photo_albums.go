package desk

import (
	"net/http"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) multiPhotoAlbums(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	albums, err := res.Lib.PhotoAlbums(r.Context())
	if err != nil {
		apiError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"albums": albums})
}
