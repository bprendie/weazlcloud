package desk

import "net/http"

func (h *Handler) photoDuplicates(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	hidden := r.URL.Query().Get("hidden") == "1"
	if r.Method == http.MethodPost {
		var body struct {
			ID string `json:"id"`
		}
		if !decodeBody(w, r, &body, 512) {
			return
		}
		if err := res.Lib.PreferPhoto(r.Context(), body.ID, hidden); err != nil {
			photoAPIError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "preferred"})
		return
	}
	page, err := res.Lib.PhotoDuplicates(r.Context(), r.URL.Query().Get("cursor"), 20, hidden)
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, page)
}
