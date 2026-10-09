package desk

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) mobileSourceRecoveryWrite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, 405, map[string]string{"code": "method_not_allowed"})
		return
	}
	res, user, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var in struct {
		DeviceID string `json:"device_id"`
		catalog.SourceRecoveryAdoption
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(w, 400, map[string]string{"code": "invalid_request"})
		return
	}
	device, err := h.mobileBackupDevice(r, user, in.DeviceID)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if _, err = h.mobilePartGrant(r, user, device, "photo"); err != nil {
		apiUsersError(w, err)
		return
	}
	ctx, guard, cancel, err := h.photoUploadCommitGuard(r, res)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	defer cancel()
	if r.Header.Get("Authorization") == "" {
		guard = func(publish func() error) error {
			grant, e := h.mobilePartGrant(r, user, device, "photo")
			if e != nil {
				return e
			}
			return h.users.WithDeviceGrant(grant, publish, users.PhotosWrite)
		}
	}
	m, err := res.Lib.AdoptPhotoSourceCollection(ctx, device, in.SourceRecoveryAdoption, guard)
	if err != nil {
		status, code := 503, "service_unavailable"
		switch {
		case errors.Is(err, catalog.ErrAlbumInvalid):
			status, code = 400, "invalid_request"
		case errors.Is(err, catalog.ErrNotFound):
			status, code = 404, "source_not_found"
		case errors.Is(err, catalog.ErrRevisionMismatch):
			status, code = 409, "stale_revision"
		case errors.Is(err, catalog.ErrSourceConflict):
			status, code = 409, "mapping_conflict"
		case errors.Is(err, vault.ErrLocked):
			status, code = 423, "vault_locked"
		case errors.Is(err, users.ErrNoSession), errors.Is(err, users.ErrInsufficientScope):
			status, code = 403, "device_authorization_required"
		}
		writeJSON(w, status, map[string]string{"code": code})
		return
	}
	writeJSON(w, 200, map[string]any{"mapping": m})
}
