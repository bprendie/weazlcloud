package desk

import (
	"net/http"

	"github.com/bprendie/weazlcloud/internal/vault"
)

type copyRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (h *Handler) copyLibrary(w http.ResponseWriter, r *http.Request) {
	if h.lib == nil || h.vault == nil || !h.vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body copyRequest
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	if err := h.lib.Copy(r.Context(), body.From, body.To); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "copied"})
}

func (h *Handler) multiCopy(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body copyRequest
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	if err := res.Lib.Copy(r.Context(), body.From, body.To); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "copied"})
}
