package desk

import (
	"net/http"
	"os"
	"strings"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type archiveRequest struct {
	Paths []string `json:"paths"`
}

func (h *Handler) multiCreateArchive(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body archiveRequest
	if !decodeBody(w, r, &body, 1<<20) {
		return
	}
	job, err := res.Archives.Start(body.Paths)
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, archiveView(job))
}

func (h *Handler) multiArchive(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	id := r.URL.Query().Get("id")
	job, path, ok := res.Archives.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "archive job not found"})
		return
	}
	if r.URL.Query().Get("download") == "" || job.Status != "ready" {
		writeJSON(w, http.StatusOK, archiveView(job))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": "archive expired"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": "archive expired"})
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="weazlcloud-`+safeArchiveID(id)+`.zip"`)
	http.ServeContent(w, r, "weazlcloud-"+safeArchiveID(id)+".zip", info.ModTime(), f)
}

func (h *Handler) multiCancelArchive(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	if !res.Archives.Cancel(r.URL.Query().Get("id")) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "archive job is not cancellable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "cancelled"})
}

func archiveView(job filesvc.ArchiveJobView) map[string]any {
	out := map[string]any{
		"id":         job.ID,
		"status":     job.Status,
		"files":      job.Files,
		"bytes":      job.Bytes,
		"created_at": job.CreatedAt,
		"expires_at": job.ExpiresAt,
	}
	if job.Error != "" {
		out["error"] = job.Error
	}
	return out
}

func safeArchiveID(id string) string {
	if id == "" || strings.Trim(id, "0123456789abcdef") != "" {
		return "archive"
	}
	if len(id) > 32 {
		return id[:32]
	}
	return id
}
