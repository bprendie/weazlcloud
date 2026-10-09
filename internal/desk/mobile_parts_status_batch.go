package desk

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/vault"
)

const mobileStatusBatchLimit = 100

func (h *Handler) mobilePartsStatusBatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"code": "method_not_allowed"})
		return
	}
	h.multiGuard(w, r, true, func(w http.ResponseWriter, r *http.Request) {
		res, user, e := h.currentResource(r)
		if e != nil {
			apiUsersError(w, e)
			return
		}
		if !res.Vault.Unlocked() {
			mobilePartError(w, vault.ErrLocked)
			return
		}
		device, e := h.mobileBackupDevice(r, user, r.URL.Query().Get("device_id"))
		if e != nil {
			apiUsersError(w, e)
			return
		}
		if _, e = h.mobilePartGrant(r, user, device, "photo"); e != nil {
			apiUsersError(w, e)
			return
		}
		var in struct {
			IDs []string `json:"upload_ids"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&in); e != nil {
			mobilePartError(w, mobileparts.ErrInvalid)
			return
		}
		if e = decoder.Decode(new(any)); e != io.EOF {
			mobilePartError(w, mobileparts.ErrInvalid)
			return
		}
		seen := map[string]bool{}
		if len(in.IDs) < 1 || len(in.IDs) > mobileStatusBatchLimit {
			mobilePartError(w, mobileparts.ErrInvalid)
			return
		}
		for _, id := range in.IDs {
			b, e := hex.DecodeString(id)
			if e != nil || len(b) != 16 || seen[id] {
				mobilePartError(w, mobileparts.ErrInvalid)
				return
			}
			seen[id] = true
		}
		items := make([]map[string]any, 0, len(in.IDs))
		pending := false
		for _, id := range in.IDs {
			v, e := h.mobileParts.StatusKind(res, id, user.ID, device, "photo")
			if e != nil {
				code := "status_unavailable"
				if errors.Is(e, mobileparts.ErrNotFound) {
					code = "not_found"
				} else if errors.Is(e, mobileparts.ErrExpired) {
					code = "staging_expired"
				} else if errors.Is(e, mobileparts.ErrCorrupt) {
					code = "staging_corrupt"
				}
				items = append(items, map[string]any{"upload_id": id, "code": code})
				continue
			}
			if v.Status == "uploading" || v.Status == "queued" || v.Status == "verifying" {
				pending = true
			}
			items = append(items, map[string]any{"upload_id": id, "transfer": v})
		}
		w.Header().Set("Cache-Control", "no-store")
		if pending {
			w.Header().Set("Retry-After", "5")
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
}
