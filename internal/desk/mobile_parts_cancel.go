package desk

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

func (h *Handler) cancelMobileParts(w http.ResponseWriter, r *http.Request, res *filesvc.Resource, user users.User, device, id, kind string) {
	grant, err := h.mobilePartGrant(r, user, device, kind)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	guard := func(publish func() error) error { return h.users.WithDeviceGrant(grant, publish, partScopes(kind)...) }
	view, err := h.mobileParts.CancelCoordinated(res, id, device, func(s mobileparts.Session) (json.RawMessage, error) {
		if s.Spec.Kind != kind {
			return nil, mobileparts.ErrNotFound
		}
		if kind == "photo" {
			err := h.photoUploads.Cancel(res, user, id, device)
			if errors.Is(err, upload.ErrIdempotencyConflict) {
				receipt, statusErr := h.photoUploads.Status(res, user, id, device)
				if statusErr == nil && receipt.Status == "stored" {
					return json.Marshal(receipt)
				}
			}
			return nil, err
		}
		m := backup.New(h.uploads)
		m.SetCommitGuard(guard)
		receipt, err := m.Status(r.Context(), res, user, id, device)
		if err != nil {
			return nil, err
		}
		if receipt.Status == "stored" {
			return json.Marshal(receipt)
		}
		return nil, m.Cancel(r.Context(), res, user, id, device)
	})
	if err != nil {
		mobilePartError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if view.Status == "stored" {
		writeJSON(w, 409, map[string]any{"code": "already_stored", "transfer": view})
		return
	}
	writeJSON(w, 200, map[string]any{"status": "cancelled", "transfer": view})
}
