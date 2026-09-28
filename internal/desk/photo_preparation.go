package desk

import (
	"github.com/bprendie/weazlcloud/internal/library"
	"net/http"
)

func (h *Handler) multiPhotoPreparation(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var state any
	if r.Method == http.MethodGet {
		state, err = res.Lib.PhotoPreparation()
	} else {
		var body struct {
			Action string `json:"action"`
		}
		if !decodeBody(w, r, &body, 1024) {
			return
		}
		state, err = res.Lib.SetPhotoPreparation(r.Context(), body.Action)
	}
	if err != nil {
		if err == library.ErrPhotoPreparationAction {
			http.Error(w, `{"error":"unsupported photo preparation action"}`, http.StatusBadRequest)
			return
		}
		apiError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, state)
}
