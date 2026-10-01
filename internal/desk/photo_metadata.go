package desk

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/bprendie/weazlcloud/internal/library"
)

func (h *Handler) photoMetadataJob(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet && r.URL.Query().Get("report") == "1" {
		cursor := 0
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			cursor, err = strconv.Atoi(raw)
		}
		if err != nil {
			http.Error(w, `{"error":"invalid report cursor"}`, 400)
			return
		}
		entries, err := res.Lib.PhotoMetadataReport(cursor, 100)
		if err != nil {
			photoAPIError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"entries": entries, "next_cursor": cursor + len(entries)})
		return
	}
	var state library.PhotoMetadataJob
	if r.Method == http.MethodGet {
		state, err = res.Lib.PhotoMetadataStatus()
	} else {
		var body struct {
			Action string `json:"action"`
			library.PhotoMetadataOptionsJob
		}
		if !decodeBody(w, r, &body, 4096) {
			return
		}
		state, err = res.Lib.SetPhotoMetadataJob(r.Context(), body.Action, body.PhotoMetadataOptionsJob)
	}
	if err != nil {
		if errors.Is(err, library.ErrMetadataBusy) {
			http.Error(w, `{"error":"metadata job active with different options"}`, 409)
		} else if errors.Is(err, library.ErrMetadataAction) {
			http.Error(w, `{"error":"invalid metadata job action or options"}`, 400)
		} else {
			photoAPIError(w, err)
		}
		return
	}
	writeJSON(w, 200, state)
}
