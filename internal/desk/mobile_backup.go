package desk

import (
	"errors"
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// tryMobileBackups is wired by the parent before the generic Files router.
// Independent parts routes are left to the parent's encrypted transport.
// multiGuard/users.Current must enforce files:write AND backup:write for bearer
// requests. The existing legacy Photos allowlist fails closed until MS1 wiring.
func (h *Handler) tryMobileBackups(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if p != "/api/v1/backups/sources" && !strings.HasPrefix(p, "/api/v1/backups/sources/") && p != "/api/v1/backups/uploads" && !strings.HasPrefix(p, "/api/v1/backups/uploads/") {
		return false
	}
	if strings.Contains(p, "/parts/") {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if h.users == nil || h.registry == nil {
		writeJSON(w, 404, map[string]string{"error": "backup resource not found"})
		return true
	}
	h.multiGuard(w, r, true, func(w http.ResponseWriter, r *http.Request) {
		res, user, err := h.currentResource(r)
		if err != nil {
			apiUsersError(w, err)
			return
		}
		if !res.Vault.Unlocked() {
			mobileBackupError(w, vault.ErrLocked)
			return
		}
		m := backup.New(h.uploads)
		if r.Header.Get("Authorization") != "" {
			grant, err := h.users.GrantForRequest(r, users.BackupWrite, users.FilesWrite)
			if err != nil {
				apiUsersError(w, err)
				return
			}
			m.SetCommitGuard(func(publish func() error) error {
				return h.users.WithDeviceGrant(grant, publish, users.BackupWrite, users.FilesWrite)
			})
		}
		if strings.HasPrefix(p, "/api/v1/backups/sources") {
			h.mobileBackupSources(w, r, m, res, user)
			return
		}
		h.mobileBackupUploads(w, r, m, res, user)
	})
	return true
}

// Cookie callers explicitly select an enrolled owner device. Bearer callers
// are always bound to the stable authenticated device ID, never a supplied label.
func (h *Handler) mobileBackupDevice(r *http.Request, user users.User, requested string) (string, error) {
	if r.Header.Get("Authorization") != "" {
		device, err := h.users.DeviceForRequest(r)
		if err != nil {
			return "", err
		}
		if requested != "" && requested != device.ID {
			return "", backup.ErrNotFound
		}
		return device.ID, nil
	}
	if _, ok := h.users.ActiveDevice(user.ID, requested); ok {
		return requested, nil
	}
	return "", backup.ErrNotFound
}

func mobileBackupError(w http.ResponseWriter, err error) {
	status, code := http.StatusBadRequest, "invalid_request"
	switch {
	case errors.Is(err, backup.ErrUnsupportedVersion):
		status, code = 409, "unsupported_format"
	case errors.Is(err, backup.ErrNotFound), errors.Is(err, catalog.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, backup.ErrIdempotencyConflict):
		status, code = 409, "idempotency_conflict"
	case errors.Is(err, catalog.ErrRevisionMismatch), errors.Is(err, catalog.ErrConflict), errors.Is(err, catalog.ErrDescendant):
		status, code = 409, "stale_revision"
	case errors.Is(err, backup.ErrPaused):
		status, code = 409, "source_inactive"
	case errors.Is(err, backup.ErrChecksum):
		status, code = 409, "checksum_mismatch"
	case errors.Is(err, vault.ErrLocked):
		writeJSON(w, 423, map[string]string{"error": err.Error(), "code": "vault_locked"})
		return
	case errors.Is(err, backup.ErrInvalid):
	default:
		uploadError(w, err)
		return
	}
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
}

func (h *Handler) mobileBackupSources(w http.ResponseWriter, r *http.Request, m *backup.Manager, res *filesvc.Resource, user users.User) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/backups/sources")
	if p == "" && r.Method == http.MethodPost {
		var src backup.Source
		if !decodeBody(w, r, &src, 32<<10) {
			return
		}
		device, err := h.mobileBackupDevice(r, user, src.DeviceID)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		out, err := m.Register(r.Context(), res, user, device, src)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		writeJSON(w, 201, out)
		return
	}
	device, err := h.mobileBackupDevice(r, user, r.URL.Query().Get("device_id"))
	if err != nil {
		mobileBackupError(w, err)
		return
	}
	if p == "" && r.Method == http.MethodGet {
		out, next, err := m.Sources(r.Context(), res, user, device, r.URL.Query().Get("after"))
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"sources": out, "next": next})
		return
	}
	if strings.HasPrefix(p, "/") && len(p) > 1 && r.Method == http.MethodPatch {
		var body struct {
			Status   string `json:"status"`
			Revision uint64 `json:"expected_revision"`
		}
		if !decodeBody(w, r, &body, 4096) {
			return
		}
		out, err := m.UpdateSource(r.Context(), res, user, device, p[1:], body.Status, body.Revision)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	writeJSON(w, 405, map[string]string{"error": "unsupported backup source operation"})
}
