package desk

import "net/http"

func (h *Handler) multiPhotoFolderVisibility(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		Path   string `json:"path"`
		Hidden *bool  `json:"hidden"`
	}
	if !decodeBody(w, r, &body, 16<<10) || body.Hidden == nil {
		return
	}
	updated, err := res.Lib.SetPhotoFolderHidden(r.Context(), body.Path, *body.Hidden)
	if err != nil {
		photoAPIError(w, err)
		return
	}
	view := struct {
		Path   string `json:"path"`
		Hidden bool   `json:"hidden"`
	}{Path: updated.Path, Hidden: updated.Hidden}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}
