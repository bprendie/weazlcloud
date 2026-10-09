package desk

import (
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/library"
)

// tryMobileIdentity is called by the parent before serveMulti's switch. It owns
// only discovery and narrow identity routes; all data routes use existing guards.
func (h *Handler) tryMobileIdentity(w http.ResponseWriter, r *http.Request) bool {
	if h.users == nil {
		return false
	}
	path := r.URL.Path
	if path == "/.well-known/weazlcloud" {
		if r.Method != "GET" {
			mobileMethodError(w)
			return true
		}
		h.mobileDiscovery(w, r)
		return true
	}
	var fn func(http.ResponseWriter, *http.Request)
	switch path {
	case "/api/v1/mobile/capabilities", "/api/v1/mobile/profile", "/api/v1/mobile/status":
		if r.Method != "GET" {
			mobileMethodError(w)
			return true
		}
		fn = h.mobileCapabilities
	case "/api/v1/devices":
		if r.Method != "GET" && r.Method != "POST" {
			mobileMethodError(w)
			return true
		}
		fn = h.photoDevices
	case "/api/v1/devices/revoke":
		if r.Method != "POST" {
			mobileMethodError(w)
			return true
		}
		fn = h.revokePhotoDevice
	default:
		if !strings.HasPrefix(path, "/api/v1/devices/") {
			return false
		}
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/devices/"), "/")
		if len(parts) == 1 && parts[0] != "" && r.Method == "GET" {
			fn = h.mobileDeviceStatus
		} else if len(parts) == 2 && parts[0] != "" && r.Method == "POST" {
			switch parts[1] {
			case "rotate":
				fn = h.mobileDeviceRotate
			case "reauthorize":
				fn = h.mobileDeviceReauthorize
			case "revoke":
				fn = h.mobileDeviceRevoke
			}
		}
		if fn == nil {
			mobileMethodError(w)
			return true
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if _, err := h.users.Current(r); err != nil {
		mobileIdentityError(w, err)
		return true
	}
	// Existing guard supplies owner drain lease, current account check and CSRF.
	h.multiGuard(w, r, true, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		fn(w, r)
	})
	return true
}

// TryMobileIdentity is also available to an external router integration.
func (h *Handler) TryMobileIdentity(w http.ResponseWriter, r *http.Request) bool {
	return h.tryMobileIdentity(w, r)
}
func mobileMethodError(w http.ResponseWriter) {
	writeJSON(w, 405, map[string]string{"error": "unsupported identity operation", "code": "method_not_allowed"})
}
func (h *Handler) mobileDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	id, err := h.users.InstanceID()
	if err != nil {
		mobileIdentityError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"instance_id": id, "contract_version": 1, "capabilities_path": "/api/v1/mobile/capabilities"})
}
func (h *Handler) mobileCapabilities(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		mobileIdentityError(w, err)
		return
	}
	id, err := h.users.InstanceID()
	if err != nil {
		mobileIdentityError(w, err)
		return
	}
	out := map[string]any{"instance_id": id, "contract_version": 1, "owner_id": u.ID, "profile": map[string]string{"username": u.Username, "full_name": u.FullName}, "vault_unlocked": res.Vault.Unlocked(), "features": map[string]bool{"upload_status_batch_v1": h.mobileParts != nil, "photo_content_lookup": true, "scoped_devices": true, "credential_rotation": true, "photo_collections": true, "source_collections": true, "source_collection_recovery": true, "source_memberships": true, "file_reads": true, "files_sync": true, "recurring_backups": true, "idempotent_grabs": true, "parts_v1": h.mobileParts != nil, "auto_finalize": h.mobileParts != nil}, "transports": []string{"ordered-patch-v1", "parts-v1"}, "limits": map[string]any{"source_collection_lookup_items": 200, "source_collection_lookup_matches": 200, "upload_status_batch_items": mobileStatusBatchLimit, "upload_status_poll_seconds": 5, "mobile_receive_workers": h.mobileParts.ReceiveLimits().Global, "mobile_receive_workers_per_owner": h.mobileParts.ReceiveLimits().PerOwner, "mobile_pending_uploads_per_owner": h.mobileParts.ReceiveLimits().Pending, "mobile_pending_bytes_per_owner": h.mobileParts.ReceiveLimits().PendingBytes, "recommended_upload_concurrency": min(4, h.mobileParts.ReceiveLimits().PerOwner), "photo_lookup_items": library.PhotoContentLookupLimit, "photo_lookup_matches": library.PhotoContentMatchLimit, "active_devices": 32, "credential_ttl_seconds": 7776000, "rotation_grace_seconds": 900, "staging_expiry_seconds": 86400, "ordered_chunk_bytes": 16 << 20, "part_bytes": 16 << 20, "missing_parts_page": 200, "mobile_finalize_workers": h.mobileFinalizerLimits().global, "mobile_finalize_workers_per_owner": h.mobileFinalizerLimits().perOwner}}
	if r.Header.Get("Authorization") != "" {
		d, err := h.users.DeviceForRequest(r)
		if err != nil {
			mobileIdentityError(w, err)
			return
		}
		out["device_id"], out["generation"], out["grants"], out["expires_at"] = d.ID, d.Generation, d.GrantedScopes(), d.ExpiresAt
	} else {
		out["authentication"] = "account_session"
	}
	writeJSON(w, 200, out)
}
