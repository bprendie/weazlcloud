package desk

import (
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/users"
)

func (h *Handler) mobileBackupUploads(w http.ResponseWriter, r *http.Request, m *backup.Manager, res *filesvc.Resource, user users.User) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/backups/uploads")
	if p == "" && r.Method == http.MethodPost {
		var spec backup.Spec
		if !decodeBody(w, r, &spec, 32<<10) {
			return
		}
		device, err := h.mobileBackupDevice(r, user, spec.DeviceID)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		out, err := m.Create(r.Context(), res, user, device, spec)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		writeJSON(w, 201, out)
		return
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) < 1 || len(parts) > 2 || len(parts[0]) != 32 {
		mobileBackupError(w, backup.ErrNotFound)
		return
	}
	device, err := h.mobileBackupDevice(r, user, r.URL.Query().Get("device_id"))
	if err != nil {
		mobileBackupError(w, err)
		return
	}
	if len(parts) == 2 && parts[1] == "finalize" && r.Method == http.MethodPost {
		out, err := m.Finalize(r.Context(), res, user, parts[0], device)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	if len(parts) != 1 {
		writeJSON(w, 405, map[string]string{"error": "unsupported backup upload operation"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		out, err := m.Status(r.Context(), res, user, parts[0], device)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		writeJSON(w, 200, out)
	case http.MethodDelete:
		if err := m.Cancel(r.Context(), res, user, parts[0], device); err != nil {
			mobileBackupError(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "cancelled"})
	case http.MethodPatch:
		offset, err := parseUploadOffset(r.Header.Get("Upload-Offset"))
		if err != nil {
			uploadError(w, err)
			return
		}
		out, err := m.Append(r.Context(), res, user, parts[0], device, offset, r.ContentLength, strings.ToLower(r.Header.Get("Upload-Chunk-SHA256")), r.Body)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		writeJSON(w, 200, out)
	default:
		writeJSON(w, 405, map[string]string{"error": "unsupported backup upload operation"})
	}
}
