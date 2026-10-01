package desk

import (
	"errors"
	"net/http"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

func (h *Handler) photoSelectionCreate(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body library.PhotoSelectionOptions
	if !decodeBody(w, r, &body, 2<<20) {
		return
	}
	selection, err := res.Lib.CreatePhotoSelection(r.Context(), body)
	if err != nil {
		photoSelectionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, selection)
}

func (h *Handler) photoSelectionAction(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		Selection     string `json:"selection_id"`
		Action        string `json:"action"`
		Hidden        bool   `json:"hidden"`
		ConfirmHidden bool   `json:"confirm_hidden"`
		AlbumID       string `json:"album_id"`
		Revision      uint64 `json:"revision"`
	}
	if !decodeBody(w, r, &body, 8192) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch body.Action {
	case "archive":
		if body.Hidden && !body.ConfirmHidden {
			writeJSON(w, 400, map[string]string{"error": "confirm download of the selected hidden photos"})
			return
		}
		manifest, err := res.Lib.PreparePhotoSelectionArchive(r.Context(), body.Selection, body.Hidden)
		if err != nil {
			photoSelectionError(w, err)
			return
		}
		job, err := res.Archives.StartManifest(manifest)
		if err != nil {
			apiError(w, err)
			return
		}
		writeJSON(w, 202, job)
	case "add_album", "remove_album":
		album, err := res.Lib.ChangePhotoSelectionAlbum(r.Context(), body.Selection, body.AlbumID, body.Revision, body.Hidden, body.Action == "remove_album")
		if err != nil {
			photoSelectionError(w, err)
			return
		}
		header, err := res.Lib.PhotoAlbumHeader(r.Context(), album.ID, body.Hidden)
		if err != nil {
			photoSelectionError(w, err)
			return
		}
		writeJSON(w, 200, header)
	case "delete":
		count, err := res.Lib.DeletePhotoSelection(r.Context(), body.Selection, body.Hidden)
		if err != nil {
			photoSelectionError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": count})
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported selection action"})
	}
}

func photoSelectionError(w http.ResponseWriter, err error) {
	if errors.Is(err, library.ErrPhotoCursorStale) || errors.Is(err, catalog.ErrRevisionMismatch) {
		writeJSON(w, 409, map[string]string{"error": "the selection changed or expired; select the photos again"})
		return
	}
	if errors.Is(err, catalog.ErrNotFound) || errors.Is(err, catalog.ErrAlbumNotFound) {
		writeJSON(w, 404, map[string]string{"error": "photo selection or album not found"})
		return
	}
	apiError(w, err)
}
