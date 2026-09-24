package desk

import "net/http"

func (h *Handler) multiMusicLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	preview, err := res.Lib.Music(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		apiError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preview)
}
