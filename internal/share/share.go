package share

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/headers"
	"github.com/bprendie/weazlcloud/internal/ratelimit"
	"github.com/bprendie/weazlcloud/internal/ready"
)

type Handler struct {
	store *capsule.Store
	limit *ratelimit.Limiter
}

func New(store *capsule.Store) *Handler {
	return &Handler{store: store, limit: ratelimit.New(time.Minute, 12, 4096)}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers.Secure(w)
	if r.URL.Path == "/live" && r.Method == http.MethodGet {
		ready.Live(w, r)
		return
	}
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
	if id == "" || len(id) != 32 {
		return "", ""
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return "", ""
		}
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
	key := id + ":" + remoteHost(r)
	if !h.limit.Allow(key) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many grab attempts; try again later"})
		return
	}
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
	var rec capsule.Record
	rec, err := h.store.StreamGrab(id, phrase, func(grab capsule.Record) (io.Writer, error) {
		rec = grab
		remaining := rec.Limit - rec.Used
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("X-Weazl-Grabs-Remaining", strconv.Itoa(remaining))
		name := rec.Name
		if rec.Kind == "folder" && !strings.HasSuffix(strings.ToLower(name), ".zip") {
			name += ".zip"
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeFilename(name)+`"`)
		w.WriteHeader(http.StatusOK)
		return w, nil
	})
	if err != nil {
		if !errors.Is(err, capsule.ErrPhrase) {
			h.limit.Reset(key)
		}
		writeGone(w, err)
		return
	}
	h.limit.Reset(key)
}

func writeGone(w http.ResponseWriter, err error) {
	status := http.StatusNotFound
	if errors.Is(err, capsule.ErrPhrase) {
		status = http.StatusUnauthorized
	}
	if errors.Is(err, capsule.ErrStorage) {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	msg := "this grab is gone"
	if errors.Is(err, capsule.ErrPhrase) {
		msg = "incorrect passphrase"
	} else if errors.Is(err, capsule.ErrStorage) {
		msg = "the grab could not be completed; try again later"
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "status": "burned"})
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func safeFilename(name string) string {
	name = strings.NewReplacer("\r", "", "\n", "", `"`, "", "\\", "_").Replace(name)
	if name == "" {
		return "grab"
	}
	return name
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
