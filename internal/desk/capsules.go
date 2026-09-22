package desk

import (
	"archive/zip"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
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
	_, _ = h.caps.CleanupExpired(time.Now().UTC())
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
	rec, source, err := h.seal(r, body)
	if err != nil {
		apiError(w, err)
		return
	}
	got, err := h.caps.MintStream(rec, body.Passphrase, source)
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

func (h *Handler) seal(r *http.Request, body mintBody) (capsule.Record, capsule.StreamSource, error) {
	return h.sealFor(r, body, h.vault, h.lib)
}

func (h *Handler) sealFor(r *http.Request, body mintBody, v *vault.Vault, l *library.Library) (capsule.Record, capsule.StreamSource, error) {
	rec := capsule.Record{
		Label: body.Label, Name: body.Path, Kind: body.Kind, Gate: body.Gate,
		Expires: time.Now().Add(parseExpiry(body.Expiry)), Limit: body.Grabs,
	}
	if rec.Label == "" {
		rec.Label = "recipient"
	}
	if rec.Kind == "" {
		rec.Kind = "file"
	}
	if rec.Limit < 1 {
		rec.Limit = 1
	}
	if rec.Kind == "folder" {
		return h.sealFolderFor(r, body.Path, rec, l)
	}
	f, err := l.Metadata(r.Context(), body.Path)
	if err != nil {
		return rec, nil, err
	}
	rec.Size = f.Size
	rec.Name = path.Base(body.Path)
	rec.Files = []capsule.Member{{Title: rec.Name, Size: rec.Size, Kind: "FILE"}}
	return rec, func(dst io.Writer) error { return l.StreamTo(r.Context(), body.Path, dst) }, nil
}

func (h *Handler) sealFolderFor(r *http.Request, prefix string, rec capsule.Record, l *library.Library) (capsule.Record, capsule.StreamSource, error) {
	if err := l.Ensure(r.Context()); err != nil {
		return rec, nil, err
	}
	var selected []catalog.File
	for _, f := range l.List() {
		if f.Path != prefix && !strings.HasPrefix(f.Path, strings.TrimSuffix(prefix, "/")+"/") {
			continue
		}
		if f.Folder {
			continue
		}
		selected = append(selected, f)
		rec.Files = append(rec.Files, capsule.Member{Title: path.Base(f.Path), Size: f.Size, Kind: "FILE"})
		rec.Size += f.Size
	}
	if len(rec.Files) == 0 {
		return rec, nil, capsule.ErrNeedPath
	}
	rec.Name = path.Base(prefix)
	return rec, func(dst io.Writer) error {
		z := zip.NewWriter(dst)
		for _, f := range selected {
			name := strings.TrimPrefix(f.Path, strings.TrimSuffix(prefix, "/")+"/")
			if name == f.Path {
				name = path.Base(f.Path)
			}
			entry, err := z.Create(name)
			if err != nil {
				return err
			}
			if err := l.StreamTo(r.Context(), f.Path, entry); err != nil {
				return err
			}
		}
		return z.Close()
	}, nil
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
