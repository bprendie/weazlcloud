package desk

import (
	"net/http"
	"strconv"
)

func (h *Handler) multiPhotoFailures(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	start := 0
	if value := r.URL.Query().Get("start"); value != "" {
		start, err = strconv.Atoi(value)
		if err != nil || start < 0 {
			http.Error(w, "invalid report offset", 400)
			return
		}
	}
	report, err := res.Lib.PhotoFailures(r.Context(), start)
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, report)
}
