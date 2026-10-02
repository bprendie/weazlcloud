package desk

import (
	"bytes"
	"net/http"
	"time"
)

func (h *Handler) multiLivePhotos(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == "GET" {
		job, err := res.Lib.LivePhotoStatus(r.URL.Query().Get("report") == "1")
		if err != nil {
			photoAPIError(w, err)
			return
		}
		writeJSON(w, 200, job)
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	job, err := res.Lib.SetLivePhotoJob(r.Context(), body.Action)
	if err != nil {
		photoAPIError(w, err)
		return
	}
	writeJSON(w, 202, job)
}
func (h *Handler) multiLivePair(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	var body struct {
		Still          string `json:"still_id"`
		Motion         string `json:"motion_id"`
		StillRevision  uint64 `json:"still_revision"`
		MotionRevision uint64 `json:"motion_revision"`
		Hidden         bool   `json:"hidden"`
		Unlink         bool   `json:"unlink"`
	}
	if !decodeBody(w, r, &body, 4096) {
		return
	}
	item, err := res.Lib.LinkLivePhoto(r.Context(), body.Still, body.Motion, body.StillRevision, body.MotionRevision, body.Hidden, body.Unlink)
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, item)
}
func (h *Handler) multiLiveMotion(w http.ResponseWriter, r *http.Request, id string) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	body, err := res.Lib.LiveMotion(r.Context(), id, r.URL.Query().Get("hidden") == "1")
	if err != nil {
		photoAPIError(w, err)
		return
	}
	defer clear(body)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, "motion.mp4", time.Time{}, bytes.NewReader(body))
}
