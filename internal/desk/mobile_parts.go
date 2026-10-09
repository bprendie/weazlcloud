package desk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/mobileparts"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// mobileIntent is private, owner-encrypted durable authorization and intent.
type mobileIntent struct {
	Grant users.DeviceGrant `json:"grant"`
	Photo json.RawMessage   `json:"photo,omitempty"`
	File  json.RawMessage   `json:"file,omitempty"`
}

func partScopes(kind string) []string {
	if kind == "photo" {
		return []string{users.PhotosWrite}
	}
	return []string{users.FilesWrite, users.BackupWrite}
}
func (h *Handler) mobilePartGrant(r *http.Request, user users.User, device, kind string) (users.DeviceGrant, error) {
	scopes := partScopes(kind)
	if r.Header.Get("Authorization") != "" {
		return h.users.GrantForRequest(r, scopes...)
	}
	d, ok := h.users.ActiveDevice(user.ID, device)
	if !ok {
		return users.DeviceGrant{}, users.ErrNoSession
	}
	for _, scope := range scopes {
		if !users.HasScope(d, scope) {
			return users.DeviceGrant{}, users.ErrInsufficientScope
		}
	}
	return users.DeviceGrant{OwnerID: user.ID, DeviceID: d.ID, AuthorizationVersion: d.AuthorizationVersion, ExpiresAt: d.ExpiresAt, Scopes: scopes}, nil
}
func (h *Handler) tryMobileParts(w http.ResponseWriter, r *http.Request) bool {
	kind, base := "photo", "/api/v1/photos/uploads"
	if strings.HasPrefix(r.URL.Path, "/api/v1/backups/uploads") {
		kind, base = "file", "/api/v1/backups/uploads"
	}
	if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
		return false
	}
	if r.URL.Path == base {
		if r.Method != http.MethodPost {
			return false
		}
		// Detect negotiation without altering legacy semantics or consuming its body.
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if e != nil {
			mobilePartError(w, mobileparts.ErrInvalid)
			return true
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var transport struct {
			Transport string `json:"transport"`
		}
		if json.Unmarshal(raw, &transport) != nil || transport.Transport != "parts-v1" {
			return false
		}
		h.multiGuard(w, r, true, func(w http.ResponseWriter, r *http.Request) { h.createMobileParts(w, r, kind) })
		return true
	}
	chunks := strings.Split(strings.TrimPrefix(r.URL.Path, base+"/"), "/")
	// Legacy PATCH/component finalize remain available; their coordinators check transport.
	if r.Method == http.MethodPatch {
		return false
	}
	handled := true
	h.multiGuard(w, r, true, func(w http.ResponseWriter, r *http.Request) {
		handled = true
		res, user, e := h.currentResource(r)
		if e != nil {
			apiUsersError(w, e)
			return
		}
		if !mobilePartsExists(res, chunks[0]) {
			handled = false
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
		if r.Method == http.MethodDelete && len(chunks) == 1 {
			h.cancelMobileParts(w, r, res, user, device, chunks[0], kind)
			return
		}
		v, e := h.mobileParts.Status(res, chunks[0], device)
		if errors.Is(e, mobileparts.ErrNotFound) {
			handled = false
			return
		}
		if e != nil {
			mobilePartError(w, e)
			return
		}
		if e = h.matchMobilePartKind(res, chunks[0], kind); e != nil {
			mobilePartError(w, e)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		h.mobilePartOperation(w, r, res, device, chunks, v)
	})
	return handled
}
func (h *Handler) matchMobilePartKind(res *filesvc.Resource, id, kind string) error {
	// Inspect only encrypted metadata, never return the authorization intent to clients.
	s, e := h.mobileParts.Session(res, id)
	if e != nil {
		return e
	}
	if s.Spec.Kind != kind {
		return mobileparts.ErrNotFound
	}
	return nil
}
func (h *Handler) mobilePartOperation(w http.ResponseWriter, r *http.Request, res *filesvc.Resource, device string, p []string, v mobileparts.View) {
	id := p[0]
	var e error
	switch {
	case len(p) == 5 && p[1] == "components" && p[3] == "parts" && r.Method == http.MethodPut:
		index, err := strconv.ParseInt(p[4], 10, 64)
		if err != nil {
			mobilePartError(w, mobileparts.ErrInvalid)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, mobileparts.PartSize+1)
		ctx, cancel := mobileVaultContext(r.Context(), res)
		defer cancel()
		user, err := h.users.Current(r)
		if err != nil {
			apiUsersError(w, err)
			return
		}
		session, err := h.mobileParts.Session(res, id)
		if err != nil {
			mobilePartError(w, err)
			return
		}
		grant, err := h.mobilePartGrant(r, user, device, session.Spec.Kind)
		if err != nil {
			apiUsersError(w, err)
			return
		}
		body := mobileGrantReader{src: r.Body, store: h.users, grant: grant, scopes: partScopes(session.Spec.Kind)}
		v, e = h.mobileParts.Append(ctx, res, id, device, p[2], index, r.ContentLength, r.Header.Get("X-Weazl-SHA256"), body)
	case len(p) == 2 && p[1] == "parts" && r.Method == http.MethodGet:
		cursor, limit := int64(0), 200
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			cursor, e = strconv.ParseInt(raw, 10, 64)
		}
		if raw := r.URL.Query().Get("limit"); raw != "" {
			limit, e = strconv.Atoi(raw)
		}
		if e != nil || limit < 1 || limit > 200 {
			mobilePartError(w, mobileparts.ErrInvalid)
			return
		}
		component := r.URL.Query().Get("component")
		if component == "" {
			component = "original"
		}
		page, err := h.mobileParts.Missing(res, id, device, component, cursor, limit)
		if err != nil {
			mobilePartError(w, err)
			return
		}
		writeJSON(w, 200, page)
		return
	case len(p) == 2 && (p[1] == "retry" || p[1] == "finalize") && r.Method == http.MethodPost:
		if v.Status == "stored" {
			writeJSON(w, 200, map[string]any{"transfer": v})
			return
		}
		v, e = h.mobileParts.Retry(res, id, device)
	case len(p) == 1 && r.Method == http.MethodGet:
	default:
		writeJSON(w, 405, map[string]string{"code": "method_not_allowed"})
		return
	}
	if e != nil {
		mobilePartError(w, e)
		return
	}
	if v.Status == "queued" || v.Status == "verifying" {
		w.Header().Set("Retry-After", "2")
	}
	writeJSON(w, 200, map[string]any{"transfer": v})
}
func mobilePartError(w http.ResponseWriter, e error) {
	status, code := 503, "storage_unavailable"
	switch {
	case errors.Is(e, mobileparts.ErrInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(e, users.ErrNoSession):
		status, code = 401, "device_authorization_required"
	case errors.Is(e, users.ErrInsufficientScope):
		status, code = 403, "insufficient_scope"
	case errors.Is(e, mobileparts.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(e, mobileparts.ErrConflict):
		status, code = 409, "idempotency_conflict"
	case errors.Is(e, mobileparts.ErrIncomplete):
		status, code = 409, "incomplete"
	case errors.Is(e, mobileparts.ErrChecksum):
		status, code = 422, "checksum_mismatch"
	case errors.Is(e, mobileparts.ErrExpired):
		status, code = 410, "staging_expired"
	case errors.Is(e, mobileparts.ErrCorrupt):
		status, code = 409, "staging_corrupt"
	case errors.Is(e, quota.ErrExceeded), errors.Is(e, syscall.ENOSPC):
		status, code = 507, "insufficient_storage"
	case errors.Is(e, vault.ErrLocked):
		status, code = 423, "vault_locked"
	}
	writeJSON(w, status, map[string]string{"error": e.Error(), "code": code})
}

// Format dispatch must precede device lookup and lock errors: old cookie photo
// uploads use source labels, not enrolled native device identities.
func mobilePartsExists(res *filesvc.Resource, id string) bool {
	if !mobileFilesID(id) {
		return false
	}
	_, err := os.Stat(filepath.Join(mobileparts.Root(res), id, "session.enc"))
	return err == nil
}

type mobileGrantReader struct {
	src    io.Reader
	store  *users.Store
	grant  users.DeviceGrant
	scopes []string
}

func (r mobileGrantReader) Read(p []byte) (int, error) {
	if err := r.store.CheckDeviceGrant(r.grant, r.scopes...); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if revoked := r.store.CheckDeviceGrant(r.grant, r.scopes...); revoked != nil {
		return 0, revoked
	}
	return n, err
}
