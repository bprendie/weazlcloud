package share

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/headers"
	"github.com/bprendie/weazlcloud/internal/ready"
)

type Handler struct {
	store *capsule.Store
}

func New(store *capsule.Store) *Handler { return &Handler{store: store} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers.Secure(w)
	if r.URL.Path == "/ready" && r.Method == http.MethodGet {
		ready.Serve(w, r)
		return
	}
	id, rest := grabParts(r.URL.Path)
	if id == "" || h.store == nil {
		http.Error(w, "weazlcloud: no grab", http.StatusNotFound)
		return
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		writeGrabPage(w, id)
	case rest == "meta" && r.Method == http.MethodGet:
		h.meta(w, id)
	case rest == "file" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.file(w, r, id)
	default:
		http.Error(w, "weazlcloud: no grab", http.StatusNotFound)
	}
}

func grabParts(path string) (id, rest string) {
	path = strings.TrimPrefix(path, "/")
	if !strings.HasPrefix(path, "g/") {
		return "", ""
	}
	path = strings.TrimPrefix(path, "g/")
	id, rest, _ = strings.Cut(path, "/")
	if id == "" || strings.ContainsAny(id, "./\\") {
		return "", ""
	}
	return id, rest
}

func (h *Handler) meta(w http.ResponseWriter, id string) {
	rec, err := h.store.Meta(id)
	if err != nil {
		writeGone(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": rec.ID, "name": rec.Name, "kind": rec.Kind, "gate": rec.Gate,
		"size": rec.Size, "expires": rec.Expires, "files": rec.Files,
		"status": "live",
	})
}

func (h *Handler) file(w http.ResponseWriter, r *http.Request, id string) {
	phrase := r.URL.Query().Get("passphrase")
	if r.Method == http.MethodPost {
		var body struct {
			Passphrase string `json:"passphrase"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Passphrase != "" {
			phrase = body.Passphrase
		}
	}
	plain, rec, err := h.store.Grab(id, phrase)
	if err != nil {
		writeGone(w, err)
		return
	}
	name := rec.Name
	if rec.Kind == "folder" && !strings.HasSuffix(strings.ToLower(name), ".zip") {
		name += ".zip"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+strings.ReplaceAll(name, `"`, "")+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(plain)
}

func writeGone(w http.ResponseWriter, err error) {
	status := http.StatusNotFound
	if errors.Is(err, capsule.ErrPhrase) {
		status = http.StatusUnauthorized
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	msg := "this grab is gone"
	if errors.Is(err, capsule.ErrPhrase) {
		msg = "incorrect passphrase"
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "status": "burned"})
}
