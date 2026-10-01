package desk

import (
	"net/http"
	"strconv"
)

func (h *Handler) photoAlbumMemberships(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 {
			writeJSON(w, 400, map[string]string{"error": "invalid membership page limit"})
			return
		}
	}
	page, err := res.Lib.PhotoAlbumMemberships(r.Context(), r.URL.Query().Get("id"), r.URL.Query().Get("cursor"), limit, r.URL.Query().Get("hidden") == "1")
	if err != nil {
		albumMutationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, page)
}
