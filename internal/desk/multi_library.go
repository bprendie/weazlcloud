package desk

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/recovery"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) multiUnlock(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	key := authKey("unlock", r, u.ID)
	if !h.authLimit.Allow(key) {
		rateLimitResponse(w)
		return
	}
	var b passBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if err := res.Vault.Unlock([]byte(b.Passphrase)); err != nil {
		apiError(w, err)
		return
	}
	h.authLimit.Reset(key)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "unlocked"})
}

func (h *Handler) multiLock(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	res.Archives.Lock()
	res.Vault.Lock()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "locked"})
}

func (h *Handler) multiKit(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var b passBody
	if !decodeBody(w, r, &b, 8192) {
		return
	}
	if err := res.Vault.Check([]byte(b.Passphrase)); err != nil {
		apiError(w, err)
		return
	}
	out := filepath.Join(filepath.Dir(res.Vault.Path()), "weazlcloud-recovery.wzck")
	cfg, _ := json.Marshal(map[string]string{"format": "weazlcloud-places"})
	if err := recovery.Export(out, res.Vault.Path(), cfg, []byte(b.Passphrase)); err != nil {
		apiError(w, err)
		return
	}
	if _, err := recovery.Verify(out, []byte(b.Passphrase)); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "kit"})
}

func (h *Handler) multiQuota(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if h.quota == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "quota unavailable"})
		return
	}
	q, err := h.quota.Status(h.users.Count())
	if err != nil {
		apiUsersError(w, err)
		return
	}
	out := map[string]any{"capacity": q.Capacity, "used": q.Used, "limit": q.Limit, "reserved": q.Reserved, "percent": q.Percent, "users": q.Users}
	if res.Vault.Unlocked() {
		if dedupe, logical, unique, e := res.Lib.Dedupe(r.Context()); e == nil {
			out["dedupe_percent"] = dedupe
			out["logical_bytes"] = logical
			out["unique_bytes"] = unique
			if shared, allocated, manifests, index, staging, metricsErr := res.Lib.SharedMetrics(r.Context()); metricsErr == nil && shared {
				out["dedupe_scope"] = "all live and Trash references using shared storage"
				out["shared_allocated_bytes"] = allocated
				out["shared_manifest_allocated_bytes"] = manifests
				out["shared_index_allocated_bytes"] = index
				out["shared_staging_allocated_bytes"] = staging
			}
		}
		if trash, e := res.Lib.Trash(r.Context()); e == nil {
			var bytes int64
			for _, item := range trash {
				if !item.Folder {
					bytes += item.Size
				}
			}
			out["trash_bytes"] = bytes
		}
	}
	if q.Limit > q.Used+q.Reserved {
		out["available_bytes"] = q.Limit - q.Used - q.Reserved
	} else {
		out["available_bytes"] = 0
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) multiListLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h.listLibraryFor(w, r, res.Vault, res.Lib)
}
func (h *Handler) multiGetLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h.getLibraryFor(w, r, res.Vault, res.Lib)
}

func (h *Handler) multiThumbnailLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h.thumbnailLibraryFor(w, r, res.Vault, res.Lib)
}

func (h *Handler) multiCapabilityLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h.capabilityLibraryFor(w, r, res.Vault, res.Lib)
}
func (h *Handler) multiDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	h.deleteLibraryFor(w, r, res.Vault, res.Lib)
}

func (h *Handler) multiPutLibrary(w http.ResponseWriter, r *http.Request) {
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	path := r.URL.Query().Get("path")
	if h.quota != nil {
		current := int64(0)
		if old, e := res.Lib.Metadata(r.Context(), path); e == nil {
			current = old.Size
		}
		used, e := res.Lib.Usage(r.Context())
		if e != nil {
			apiError(w, e)
			return
		}
		var guarded io.Reader
		var release func()
		if res.Lib.SharedWritesEnabled() {
			guarded, release, err = h.quota.GuardSharedWrite(u.ID, h.users.Count(), used, current, r.ContentLength, r.Body)
		} else {
			guarded, release, err = h.quota.GuardReader(u.ID, h.users.Count(), used, current, r.ContentLength, r.Body)
		}
		if err != nil {
			apiError(w, err)
			return
		}
		defer release()
		r.Body = io.NopCloser(guarded)
	}
	f, err := res.Lib.PutReader(r.Context(), path, r.Body, r.ContentLength)
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fileView{Path: f.Path, Size: f.Size, Mtime: f.Mtime})
}

func (h *Handler) multiCreateFolder(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	if err := res.Lib.Mkdir(r.Context(), body.Path); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": body.Path, "folder": true})
}

func (h *Handler) multiRename(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	if err := res.Lib.Rename(r.Context(), body.From, body.To); err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "renamed"})
}
