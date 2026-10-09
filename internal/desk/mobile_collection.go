package desk

import (
	"encoding/base64"
	"encoding/json"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
	"net/http"
	"strconv"
)

// The parent router must apply its normal owner/scope/lifecycle checks first.
func (h *Handler) tryMobileCollections(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/api/v1/photos/source-collections/recover":
		h.mobileSourceRecoveryWrite(w, r)
		return true
	case "/api/v1/photos/source-collections/lookup":
		h.mobileSourceRecovery(w, r)
		return true
	case "/api/v1/photos/collections":
		h.mobileCollections(w, r)
		return true
	case "/api/v1/photos/source-collections":
		h.mobileSourceCollections(w, r, false)
		return true
	case "/api/v1/photos/source-memberships":
		h.mobileSourceCollections(w, r, true)
		return true
	}
	return false
}

type mobileCollectionCursor struct {
	Schema   int                  `json:"schema"`
	Kind     string               `json:"kind"`
	Hidden   bool                 `json:"hidden"`
	Mode     string               `json:"mode,omitempty"`
	After    string               `json:"after"`
	Position catalog.SyncPosition `json:"position"`
}

func (h *Handler) mobileCollections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit := 200
		if value := r.URL.Query().Get("limit"); value != "" {
			n, e := strconv.Atoi(value)
			if e != nil || n < 1 || n > 200 {
				writeJSON(w, 400, map[string]string{"error": "invalid limit"})
				return
			}
			limit = n
		}
		hidden := r.URL.Query().Get("hidden") == "1"
		cursor := mobileCollectionCursor{}
		var position *catalog.SyncPosition
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			if len(raw) > 4096 {
				writeJSON(w, 400, map[string]string{"error": "invalid cursor"})
				return
			}
			b, e := base64.RawURLEncoding.DecodeString(raw)
			if e != nil {
				albumMutationError(w, catalog.ErrSyncExpired)
				return
			}
			plain, e := res.Vault.Unwrap(b)
			if e != nil {
				albumMutationError(w, e)
				return
			}
			defer clear(plain)
			if json.Unmarshal(plain, &cursor) != nil || cursor.Schema != 2 || cursor.Kind != "photo-collections" || cursor.Hidden != hidden || (cursor.Mode != "snapshot" && cursor.Mode != "delta") || cursor.Position.Epoch == "" || len(cursor.After) > 128 || cursor.Mode == "delta" && cursor.After != "" {
				albumMutationError(w, catalog.ErrSyncExpired)
				return
			}
			position = &cursor.Position
		}
		if cursor.Mode == "delta" {
			delta, e := res.Lib.PhotoCollectionChanges(r.Context(), cursor.Position, limit)
			if e != nil {
				albumMutationError(w, e)
				return
			}
			encoded, e := encodeMobileCollectionCursor(res.Vault, mobileCollectionCursor{Hidden: hidden, Mode: "delta", Position: delta.Position})
			if e != nil {
				albumMutationError(w, e)
				return
			}
			if delta.HasMore {
				delta.NextCursor = encoded
			} else {
				delta.Checkpoint = encoded
			}
			writeJSON(w, 200, delta)
			return
		}
		page, e := res.Lib.PhotoCollections(r.Context(), cursor.After, position, limit)
		if e != nil {
			albumMutationError(w, e)
			return
		}
		if page.HasMore {
			encoded, e := encodeMobileCollectionCursor(res.Vault, mobileCollectionCursor{Hidden: hidden, Mode: "snapshot", After: page.Next, Position: page.Position})
			if e != nil {
				albumMutationError(w, e)
				return
			}
			page.Next = encoded
		}
		if !page.HasMore {
			encoded, e := encodeMobileCollectionCursor(res.Vault, mobileCollectionCursor{Hidden: hidden, Mode: "delta", Position: page.Position})
			if e != nil {
				albumMutationError(w, e)
				return
			}
			page.Checkpoint = encoded
		}
		writeJSON(w, 200, page)
	case http.MethodPost:
		var body struct {
			Action   string                   `json:"action"`
			Folder   catalog.CollectionFolder `json:"folder"`
			Album    catalog.Album            `json:"album"`
			ParentID *string                  `json:"parent_id"`
		}
		if !decodeBody(w, r, &body, 64<<10) {
			return
		}
		switch body.Action {
		case "save-folder":
			folder, e := res.Lib.SavePhotoCollection(r.Context(), body.Folder)
			if e != nil {
				albumMutationError(w, e)
				return
			}
			writeJSON(w, 200, folder)
		case "delete-folder":
			if e := res.Lib.DeletePhotoCollection(r.Context(), body.Folder.ID, body.Folder.Revision); e != nil {
				albumMutationError(w, e)
				return
			}
			writeJSON(w, 200, map[string]string{"status": "deleted"})
		case "save-album":
			if body.ParentID != nil {
				body.Album.ParentID, body.Album.ParentSet = *body.ParentID, true
			}
			album, e := res.Lib.SavePhotoAlbum(r.Context(), body.Album)
			if e != nil {
				albumMutationError(w, e)
				return
			}
			album.AssetIDs = nil
			writeJSON(w, 200, album)
		default:
			writeJSON(w, 400, map[string]string{"error": "unsupported collection action"})
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h *Handler) mobileSourceCollections(w http.ResponseWriter, r *http.Request, memberships bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		DeviceID   string                    `json:"device_id"`
		Operations []catalog.SourceOperation `json:"operations"`
	}
	if !decodeBody(w, r, &body, 2<<20) {
		return
	}
	if r.Header.Get("Authorization") != "" {
		device, e := h.users.DeviceForRequest(r)
		if e != nil {
			apiUsersError(w, e)
			return
		}
		if body.DeviceID != "" && body.DeviceID != device.ID {
			writeJSON(w, 403, map[string]string{"error": "device mismatch"})
			return
		}
		body.DeviceID = device.ID
	}
	ctx, guard, cancel, e := h.photoUploadCommitGuard(r, res)
	if e != nil {
		apiUsersError(w, e)
		return
	}
	defer cancel()
	outcomes, e := res.Lib.ImportPhotoSourcesGuarded(ctx, body.DeviceID, body.Operations, memberships, guard)
	if e != nil {
		albumMutationError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"outcomes": outcomes})
}

func encodeMobileCollectionCursor(v *vault.Vault, cursor mobileCollectionCursor) (string, error) {
	cursor.Schema, cursor.Kind = 2, "photo-collections"
	if cursor.Mode == "" {
		cursor.Mode = "snapshot"
	}
	plain, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	defer clear(plain)
	b, err := v.Wrap(plain)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
