package desk

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/bprendie/weazlcloud/internal/library"
)

func (h *Handler) multiPhotoPage(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	limit := library.PhotoPageDefault
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 {
			http.Error(w, `{"error":"invalid page limit"}`, http.StatusBadRequest)
			return
		}
	}
	page, err := res.Lib.PhotoPage(r.Context(), limit, r.URL.Query().Get("cursor"), r.URL.Query().Get("album"))
	if errors.Is(err, library.ErrPhotoCursor) {
		http.Error(w, `{"error":"invalid photo cursor"}`, http.StatusBadRequest)
		return
	}
	if errors.Is(err, library.ErrPhotoCursorStale) {
		http.Error(w, `{"error":"photo library changed; reload the page"}`, http.StatusConflict)
		return
	}
	if err != nil {
		apiError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}
