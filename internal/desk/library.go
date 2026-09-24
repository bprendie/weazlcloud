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
	f, err := l.Metadata(r.Context(), path)
	if err != nil {
		apiError(w, err)
		return
	}
	if r.URL.Query().Get("preview") != "" {
		if browserNativePreview(path) {
			w.Header().Set("Content-Type", libraryContentType(path, nil))
			w.Header().Set("Content-Disposition", "inline")
			w.Header().Set("Accept-Ranges", "bytes")
			if r.Header.Get("Range") != "" {
				serveLibraryRange(w, r, l, path, f.Size, libraryContentType(path, nil))
				return
			}
			w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
			w.WriteHeader(http.StatusOK)
			if err := l.StreamTo(r.Context(), path, w); err != nil {
				return
			}
			return
		}
		if f.Size > 64<<20 {
			apiError(w, library.ErrPreviewTooLarge)
			return
		}
		b, err := l.Get(r.Context(), path)
		if err != nil {
			apiError(w, err)
			return
		}
		contentType := libraryContentType(path, b)
		if rendered, renderedType, ok := renderedPreview(path, b); ok {
			b, contentType = rendered, renderedType
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Header().Set("Content-Disposition", "inline")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
		return
	}
	contentType := libraryContentType(path, nil)
	w.Header().Set("Content-Type", contentType)
	if r.URL.Query().Get("inline") != "" {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", "attachment")
	}
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Header.Get("Range") != "" {
		serveLibraryRange(w, r, l, path, f.Size, contentType)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	w.WriteHeader(http.StatusOK)
	if err := l.StreamTo(r.Context(), path, w); err != nil {
		return
	}
}

func browserNativePreview(path string) bool {
	typeName := libraryContentType(path, nil)
	return strings.HasPrefix(typeName, "image/") || strings.HasPrefix(typeName, "audio/") || strings.HasPrefix(typeName, "video/") || typeName == "application/pdf"
}

func libraryContentType(path string, sample []byte) string {
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = map[string]string{
			".md": "text/markdown", ".markdown": "text/markdown", ".json": "application/json",
			".yaml": "application/x-yaml", ".yml": "application/x-yaml", ".log": "text/plain",
			".svg": "image/svg+xml", ".m4a": "audio/mp4", ".opus": "audio/ogg", ".webm": "video/webm",
		}[strings.ToLower(filepath.Ext(path))]
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if contentType == "application/octet-stream" && len(sample) > 0 {
		contentType = http.DetectContentType(sample)
	}
	return contentType
}

func serveLibraryRange(w http.ResponseWriter, r *http.Request, l *library.Library, path string, size int64, contentType string) {
	start, end, ok := byteRange(r.Header.Get("Range"), size)
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	length := end - start + 1
	w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(size, 10))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusPartialContent)
	_ = l.StreamRange(r.Context(), path, start, length, w)
}

func byteRange(raw string, size int64) (int64, int64, bool) {
	if size < 0 || !strings.HasPrefix(raw, "bytes=") {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(raw, "bytes="), ",")
	if len(parts) != 1 {
		return 0, 0, false
	}
	bounds := strings.SplitN(strings.TrimSpace(parts[0]), "-", 2)
	if len(bounds) != 2 || size == 0 {
		return 0, 0, false
	}
	if bounds[0] == "" {
		n, err := strconv.ParseInt(bounds[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true
	}
	start, err := strconv.ParseInt(bounds[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if bounds[1] != "" {
		end, err = strconv.ParseInt(bounds[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true
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
