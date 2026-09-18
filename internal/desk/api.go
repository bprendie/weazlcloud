package desk

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/recovery"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type passBody struct {
	Passphrase string `json:"passphrase"`
	Confirm    string `json:"confirm"`
}

func (h *Handler) status(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"forged":   h.vault != nil && h.vault.Exists(),
		"unlocked": h.vault != nil && h.vault.Unlocked(),
	})
}

func (h *Handler) forge(w http.ResponseWriter, r *http.Request) {
	if h.vault == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	pass, confirm, ok := readPass(w, r)
	if !ok {
		return
	}
	if err := h.vault.Forge([]byte(pass), []byte(confirm)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "forged"})
}

func (h *Handler) unlock(w http.ResponseWriter, r *http.Request) {
	if h.vault == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	pass, _, ok := readPass(w, r)
	if !ok {
		return
	}
	if err := h.vault.Unlock([]byte(pass)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "unlocked"})
}

func (h *Handler) lock(w http.ResponseWriter, _ *http.Request) {
	if h.vault == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	h.vault.Lock()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "locked"})
}

func (h *Handler) kit(w http.ResponseWriter, r *http.Request) {
	if h.vault == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	pass, _, ok := readPass(w, r)
	if !ok {
		return
	}
	if err := h.vault.Check([]byte(pass)); err != nil {
		apiError(w, err)
		return
	}
	out := filepath.Join(filepath.Dir(h.vault.Path()), "weazlcloud-recovery.wzck")
	cfg, _ := json.Marshal(map[string]string{"format": "weazlcloud-places"})
	if err := recovery.Export(out, h.vault.Path(), cfg, []byte(pass)); err != nil {
		apiError(w, err)
		return
	}
	if _, err := recovery.Verify(out, []byte(pass)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "kit"})
}

func readPass(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var body passBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return "", "", false
	}
	if body.Passphrase == "" {
		http.Error(w, `{"error":"passphrase must not be empty"}`, http.StatusBadRequest)
		return "", "", false
	}
	return body.Passphrase, body.Confirm, true
}

func apiError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if err == vault.ErrLocked || err == vault.ErrPass || err == vault.ErrEmpty {
		status = http.StatusUnauthorized
	}
	if err == library.ErrBadPath {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
