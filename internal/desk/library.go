package desk

import (
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type fileView struct {
	Path   string    `json:"path"`
	Folder bool      `json:"folder,omitempty"`
	Size   int64     `json:"size"`
	Mtime  time.Time `json:"mtime"`
}

func (h *Handler) listLibrary(w http.ResponseWriter, r *http.Request) {
	h.listLibraryFor(w, r, h.vault, h.lib)
}

func (h *Handler) listLibraryFor(w http.ResponseWriter, r *http.Request, v *vault.Vault, l *library.Library) {
	if l == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	if v == nil || !v.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	if err := l.Ensure(r.Context()); err != nil {
		apiError(w, err)
		return
	}
	files := l.List()
	out := make([]fileView, 0, len(files))
	for _, f := range files {
		out = append(out, fileView{Path: f.Path, Folder: f.Folder, Size: f.Size, Mtime: f.Mtime})
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

func (h *Handler) putLibrary(w http.ResponseWriter, r *http.Request) {
	if h.lib == nil || h.vault == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	path := r.URL.Query().Get("path")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"too large"}`, http.StatusRequestEntityTooLarge)
		return
	}
	f, err := h.lib.Put(r.Context(), path, body)
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fileView{Path: f.Path, Size: f.Size, Mtime: f.Mtime})
}

func (h *Handler) getLibrary(w http.ResponseWriter, r *http.Request) {
	h.getLibraryFor(w, r, h.vault, h.lib)
}

func (h *Handler) getLibraryFor(w http.ResponseWriter, r *http.Request, v *vault.Vault, l *library.Library) {
	if l == nil || v == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	path := r.URL.Query().Get("path")
	b, err := l.Get(r.Context(), path)
	if err != nil {
		apiError(w, err)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = map[string]string{
			".md": "text/markdown", ".markdown": "text/markdown", ".json": "application/json",
			".yaml": "application/x-yaml", ".yml": "application/x-yaml", ".log": "text/plain",
			".svg": "image/svg+xml", ".m4a": "audio/mp4", ".webm": "video/webm",
		}[strings.ToLower(filepath.Ext(path))]
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if contentType == "application/octet-stream" {
		contentType = http.DetectContentType(b)
	}
	if r.URL.Query().Get("preview") != "" {
		if rendered, renderedType, ok := renderedPreview(path, b); ok {
			b, contentType = rendered, renderedType
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	if r.URL.Query().Get("preview") == "" {
		w.Header().Set("Content-Disposition", "attachment")
	} else {
		w.Header().Set("Content-Disposition", "inline")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (h *Handler) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	h.deleteLibraryFor(w, r, h.vault, h.lib)
}

func (h *Handler) deleteLibraryFor(w http.ResponseWriter, r *http.Request, v *vault.Vault, l *library.Library) {
	if l == nil || v == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	path := r.URL.Query().Get("path")
	if err := l.Delete(path); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}
