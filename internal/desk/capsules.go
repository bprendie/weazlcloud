package desk

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
)

type mintBody struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Gate       string `json:"gate"`
	Passphrase string `json:"passphrase"`
	Label      string `json:"label"`
	Expiry     string `json:"expiry"`
	Grabs      int    `json:"grabs"`
}

func (h *Handler) listCapsules(w http.ResponseWriter, _ *http.Request) {
	if h.caps == nil {
		writeJSON(w, http.StatusOK, map[string]any{"capsules": []any{}})
		return
	}
	recs := h.caps.List()
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.View())
	}
	writeJSON(w, http.StatusOK, map[string]any{"capsules": out, "base": h.publicBase})
}

func (h *Handler) mintCapsule(w http.ResponseWriter, r *http.Request) {
	if h.caps == nil || h.lib == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	if strings.TrimSpace(h.publicBase) == "" || !strings.HasPrefix(h.publicBase, "https://") {
		apiError(w, capsule.ErrNeedBase)
		return
	}
	var body mintBody
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	payload, rec, err := h.seal(r, body)
	if err != nil {
		apiError(w, err)
		return
	}
	got, err := h.caps.Mint(rec, body.Passphrase, payload)
	if err != nil {
		apiError(w, err)
		return
	}
	url := strings.TrimRight(h.publicBase, "/") + "/g/" + got.ID
	view := got.View()
	view["url"] = url
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) revokeCapsule(w http.ResponseWriter, r *http.Request) {
	if h.caps == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"missing id"}`, http.StatusBadRequest)
		return
	}
	if err := h.caps.Revoke(id); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "revoked"})
}

func (h *Handler) seal(r *http.Request, body mintBody) ([]byte, capsule.Record, error) {
	rec := capsule.Record{
		Label: body.Label, Name: body.Path, Kind: body.Kind, Gate: body.Gate,
		Expires: time.Now().Add(parseExpiry(body.Expiry)), Limit: body.Grabs,
	}
	if rec.Label == "" {
		rec.Label = "Gil"
	}
	if rec.Kind == "" {
		rec.Kind = "file"
	}
	if rec.Limit < 1 {
		rec.Limit = 1
	}
	if rec.Kind == "folder" {
		return h.sealFolder(r, body.Path, rec)
	}
	b, err := h.lib.Get(r.Context(), body.Path)
	if err != nil {
		return nil, rec, err
	}
	rec.Size = int64(len(b))
	rec.Name = path.Base(body.Path)
	rec.Files = []capsule.Member{{Title: rec.Name, Size: rec.Size, Kind: "FILE"}}
	return b, rec, nil
}

func (h *Handler) sealFolder(r *http.Request, prefix string, rec capsule.Record) ([]byte, capsule.Record, error) {
	if err := h.lib.Ensure(r.Context()); err != nil {
		return nil, rec, err
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, f := range h.lib.List() {
		if f.Path != prefix && !strings.HasPrefix(f.Path, strings.TrimSuffix(prefix, "/")+"/") {
			continue
		}
		b, err := h.lib.Get(r.Context(), f.Path)
		if err != nil {
			return nil, rec, err
		}
		name := strings.TrimPrefix(f.Path, strings.TrimSuffix(prefix, "/")+"/")
		if name == f.Path {
			name = path.Base(f.Path)
		}
		w, err := z.Create(name)
		if err != nil {
			return nil, rec, err
		}
		if _, err := w.Write(b); err != nil {
			return nil, rec, err
		}
		rec.Files = append(rec.Files, capsule.Member{Title: path.Base(f.Path), Size: f.Size, Kind: "FILE"})
	}
	if err := z.Close(); err != nil {
		return nil, rec, err
	}
	if len(rec.Files) == 0 {
		return nil, rec, capsule.ErrNeedPath
	}
	rec.Size = int64(buf.Len())
	rec.Name = path.Base(prefix)
	return buf.Bytes(), rec, nil
}

func parseExpiry(v string) time.Duration {
	switch v {
	case "3d":
		return 72 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}
