package desk

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type libraryCapability struct {
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	ContentType string `json:"content_type"`
}

func (h *Handler) capabilityLibrary(w http.ResponseWriter, r *http.Request) {
	h.capabilityLibraryFor(w, r, h.vault, h.lib)
}

func (h *Handler) capabilityLibraryFor(w http.ResponseWriter, r *http.Request, v *vault.Vault, l *library.Library) {
	if l == nil || v == nil {
		http.Error(w, `{"error":"not yet"}`, http.StatusNotImplemented)
		return
	}
	if !v.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	path := r.URL.Query().Get("path")
	f, err := l.Metadata(r.Context(), path)
	if err != nil {
		apiError(w, err)
		return
	}
	var sample []byte
	if !f.Folder && f.Size <= 2<<20 && needsContentInspection(filepath.Ext(path)) {
		sample, _ = l.Prefix(r.Context(), path)
	}
	writeJSON(w, http.StatusOK, capabilityFor(path, f.Size, sample))
}

func needsContentInspection(rawExt string) bool {
	ext := strings.ToLower(rawExt)
	return !isRasterExtension(ext) && !isOfficeExtension(ext) && ext != ".pdf" && ext != ".svg" && ext != ".stl" && ext != ".3mf" && !isMediaExtension(ext)
}

func capabilityFor(path string, size int64, sample []byte) libraryCapability {
	ext := strings.ToLower(filepath.Ext(path))
	contentType := libraryContentType(path, sample)
	detected := http.DetectContentType(sample)
	if len(sample) > 0 && (strings.HasPrefix(detected, "image/") || strings.HasPrefix(detected, "audio/") || strings.HasPrefix(detected, "video/") || strings.HasPrefix(detected, "text/") || detected == "application/pdf") {
		contentType = detected
	}
	label := strings.TrimPrefix(strings.ToUpper(ext), ".")
	if label == "" {
		label = strings.ToUpper(strings.TrimPrefix(contentType, "application/"))
	}
	capability := libraryCapability{Path: path, Kind: "download", Label: label, ContentType: contentType}
	if size < 0 {
		return capability
	}
	if imageContentType(contentType) && (isRasterExtension(ext) || imageContentType(detected)) {
		capability.Kind = "thumbnail"
		return capability
	}
	if ext == ".svg" {
		capability.Kind = "text"
		capability.ContentType = "text/plain; charset=utf-8"
		return capability
	}
	if strings.HasPrefix(contentType, "audio/") || strings.HasPrefix(contentType, "video/") {
		capability.Kind = "media"
		return capability
	}
	if isMediaExtension(ext) {
		capability.Kind = "media"
		if isVideoExtension(ext) {
			capability.ContentType = "video/*"
		} else {
			capability.ContentType = "audio/*"
		}
		return capability
	}
	if contentType == "application/pdf" || ext == ".pdf" {
		capability.Kind = "pdf"
		return capability
	}
	if ext == ".stl" || ext == ".3mf" {
		capability.Kind = "model"
		return capability
	}
	if isOfficeExtension(ext) || strings.HasPrefix(contentType, "text/") || contentType == "application/json" || contentType == "application/xml" || contentType == "application/x-yaml" {
		capability.Kind = "text"
		capability.ContentType = "text/plain; charset=utf-8"
		return capability
	}
	if strings.HasPrefix(http.DetectContentType(sample), "text/") {
		capability.Kind = "text"
		capability.ContentType = "text/plain; charset=utf-8"
	}
	return capability
}

func isRasterExtension(ext string) bool {
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif"
}

func imageContentType(contentType string) bool {
	return contentType == "image/jpeg" || contentType == "image/png" || contentType == "image/gif"
}

func isOfficeExtension(ext string) bool {
	switch ext {
	case ".docx", ".xlsx", ".pptx", ".odt", ".ods", ".odp":
		return true
	default:
		return false
	}
}

func isMediaExtension(ext string) bool {
	return isAudioExtension(ext) || isVideoExtension(ext)
}

func isAudioExtension(ext string) bool {
	switch ext {
	case ".mp3", ".wav", ".flac", ".m4a", ".aac", ".ogg", ".oga":
		return true
	default:
		return false
	}
}

func isVideoExtension(ext string) bool {
	switch ext {
	case ".mp4", ".mov", ".webm", ".mkv", ".avi", ".m4v":
		return true
	default:
		return false
	}
}

func isTextExtension(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".json", ".xml", ".yaml", ".yml", ".log", ".js", ".ts", ".go", ".py", ".sh", ".css", ".html":
		return true
	default:
		return false
	}
}
