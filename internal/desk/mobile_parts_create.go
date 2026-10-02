package desk

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/bprendie/weazlcloud/internal/backup"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/photoingest"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) createMobileParts(w http.ResponseWriter, r *http.Request, kind string) {
	res, user, e := h.currentResource(r)
	if e != nil {
		apiUsersError(w, e)
		return
	}
	if !res.Vault.Unlocked() {
		mobilePartError(w, vault.ErrLocked)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	var intent mobileIntent
	var id, device string
	var receipt any
	spec := mobileparts.Spec{Kind: kind}
	if kind == "photo" {
		var in photoingest.Spec
		if !decodeBody(w, r, &in, 64<<10) {
			return
		}
		device, e = h.mobileBackupDevice(r, user, in.DeviceID)
		if e != nil {
			apiUsersError(w, e)
			return
		}
		in.DeviceID = device
		if e = in.Normalize(); e != nil {
			apiError(w, e)
			return
		}
		intent.Grant, e = h.mobilePartGrant(r, user, device, kind)
		if e != nil {
			apiUsersError(w, e)
			return
		}
		out, err := h.photoUploads.CreateParts(r.Context(), res, user, in)
		if err != nil {
			uploadError(w, err)
			return
		}
		if out.Status == "stored" {
			writeJSON(w, 200, map[string]any{"receipt": out})
			return
		}
		id, receipt = out.Upload.ID, out
		intent.Photo, _ = json.Marshal(in)
		for _, c := range in.Components {
			spec.Components = append(spec.Components, mobileparts.Component{ID: c.ID, Size: c.Size, SHA256: c.SHA256})
		}
		spec.CommitWhenComplete = in.CommitWhenComplete
	} else {
		var in struct {
			backup.Spec
			CommitWhenComplete bool `json:"commit_when_complete"`
		}
		if !decodeBody(w, r, &in, 64<<10) {
			return
		}
		device, e = h.mobileBackupDevice(r, user, in.DeviceID)
		if e != nil {
			apiUsersError(w, e)
			return
		}
		in.DeviceID = device
		intent.Grant, e = h.mobilePartGrant(r, user, device, kind)
		if e != nil {
			apiUsersError(w, e)
			return
		}
		m := backup.New(h.uploads)
		m.SetCommitGuard(func(publish func() error) error {
			return h.users.WithDeviceGrant(intent.Grant, publish, partScopes(kind)...)
		})
		out, err := m.CreateParts(r.Context(), res, user, device, in.Spec)
		if err != nil {
			mobileBackupError(w, err)
			return
		}
		if out.Status == "cancelled" {
			mobileBackupError(w, backup.ErrNotFound)
			return
		}
		if out.Spec.Kind == "folder" {
			out, err = m.FinalizeParts(r.Context(), res, user, out.ID, device, nil)
			if err != nil {
				mobileBackupError(w, err)
				return
			}
			writeJSON(w, 201, map[string]any{"receipt": out})
			return
		}
		if out.Status == "stored" {
			writeJSON(w, 200, map[string]any{"receipt": out})
			return
		}
		id, receipt = out.ID, out
		intent.File, _ = json.Marshal(out.Spec)
		spec.Components = []mobileparts.Component{{ID: "original", Size: out.Spec.Size, SHA256: out.Spec.SHA256}}
		spec.CommitWhenComplete = in.CommitWhenComplete
	}
	spec.DeviceID = device
	spec.Payload, _ = json.Marshal(intent)
	// A rotated credential preserves the admitted authorization epoch. Repeated
	// create uses the first persisted grant, rather than changing private identity.
	if old, err := h.mobileParts.Session(res, id); err == nil || errors.Is(err, mobileparts.ErrExpired) {
		original := old.Spec
		original.Components = append([]mobileparts.Component(nil), old.Spec.Components...)
		for i := range original.Components {
			original.Components[i].ReceivedBytes = 0
			original.Components[i].ReceivedParts = 0
		}
		original.Payload = nil
		expected := spec
		expected.Payload = nil
		if !reflect.DeepEqual(original, expected) {
			mobilePartError(w, mobileparts.ErrConflict)
			return
		}
		var prior mobileIntent
		if json.Unmarshal(old.Spec.Payload, &prior) == nil {
			if h.users.CheckDeviceGrant(prior.Grant, partScopes(kind)...) == nil {
				intent.Grant = prior.Grant
				spec.Payload, _ = json.Marshal(intent)
			} else if bytes.Equal(prior.Photo, intent.Photo) && bytes.Equal(prior.File, intent.File) {
				// Fresh owner-approved credentials may resume the same immutable
				// source revision after password change or explicit reauthorization.
				if err := h.mobileParts.RebindPayload(res, id, device, old.Spec.Payload, spec.Payload); err != nil {
					mobilePartError(w, err)
					return
				}
			}
		}
	}
	out, err := h.mobileParts.Create(res, user.ID, id, spec)
	if err != nil {
		mobilePartError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"receipt": receipt, "transfer": out})
}
