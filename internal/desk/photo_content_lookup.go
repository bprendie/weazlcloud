package desk

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) photoContentLookup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, 405, map[string]string{"code": "method_not_allowed"})
		return
	}
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		mobilePartError(w, vault.ErrLocked)
		return
	}
	var body struct {
		Items         []library.PhotoContentKey `json:"items"`
		IncludeHidden bool                      `json:"include_hidden"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"code": "invalid_request", "error": "invalid lookup body"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, 400, map[string]string{"code": "invalid_request", "error": "invalid lookup body"})
		return
	}
	page, err := res.Lib.PhotoContentLookup(r.Context(), body.Items, body.IncludeHidden)
	if err != nil {
		if errors.Is(err, library.ErrPhotoContentLookup) {
			writeJSON(w, 400, map[string]string{"code": "invalid_request", "error": err.Error()})
			return
		}
		mobilePartError(w, err)
		return
	}
	writeJSON(w, 200, page)
}
