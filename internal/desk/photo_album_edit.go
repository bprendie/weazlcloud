package desk

import (
	"errors"
	"net/http"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) multiPhotoAlbumMutation(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		Selection   string   `json:"selection_id"`
		Hidden      bool     `json:"hidden"`
		Action      string   `json:"action"`
		ID          string   `json:"id"`
		Revision    uint64   `json:"revision"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		CoverID     string   `json:"cover_id"`
		Position    int      `json:"position"`
		AssetIDs    []string `json:"asset_ids"`
		AddIDs      []string `json:"add_ids"`
		RemoveIDs   []string `json:"remove_ids"`
	}
	if !decodeBody(w, r, &body, 2<<20) {
		return
	}
	switch body.Action {
	case "save":
		requested := catalog.Album{ID: body.ID, Revision: body.Revision, Title: body.Title, Description: body.Description, CoverID: body.CoverID, Position: body.Position, AssetIDs: body.AssetIDs}
		var album catalog.Album
		var saveErr error
		if body.Selection != "" {
			album, saveErr = res.Lib.SavePhotoSelectionAlbum(r.Context(), body.Selection, body.Hidden, requested)
		} else {
			album, saveErr = res.Lib.SavePhotoAlbum(r.Context(), requested)
		}
		if saveErr != nil {
			albumMutationError(w, saveErr)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		header, err := res.Lib.PhotoAlbumHeader(r.Context(), album.ID, body.Hidden)
		if err != nil {
			albumMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, header)
	case "delete":
		if err := res.Lib.DeletePhotoAlbum(r.Context(), body.ID, body.Revision); err != nil {
			albumMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	case "members":
		album, memberErr := res.Lib.ChangePhotoAlbumMembers(r.Context(), body.ID, body.Revision, body.AddIDs, body.RemoveIDs)
		if memberErr != nil {
			albumMutationError(w, memberErr)
			return
		}
		header, err := res.Lib.PhotoAlbumHeader(r.Context(), album.ID, body.Hidden)
		if err != nil {
			albumMutationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, header)
	default:
		http.Error(w, `{"error":"unsupported album action"}`, http.StatusBadRequest)
	}
}

func albumMutationError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, vault.ErrLocked) {
		status = http.StatusUnauthorized
	}
	if errors.Is(err, catalog.ErrAlbumNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, catalog.ErrRevisionMismatch) || errors.Is(err, library.ErrPhotoCursorStale) {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
