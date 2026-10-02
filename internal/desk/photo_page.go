package desk

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

func (h *Handler) multiPhotoPage(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	limit := library.PhotoPageDefault
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 {
			http.Error(w, `{"error":"invalid page limit"}`, http.StatusBadRequest)
			return
		}
	}
	var page library.PhotoPage
	if anchor := r.URL.Query().Get("around"); anchor != "" && r.URL.Query().Get("cursor") == "" {
		page, err = res.Lib.PhotoTimelinePageAround(r.Context(), limit, r.URL.Query().Get("album"), r.URL.Query().Get("mode"), r.URL.Query().Get("date"), anchor)
	} else {
		page, err = res.Lib.PhotoTimelinePage(r.Context(), limit, r.URL.Query().Get("cursor"), r.URL.Query().Get("album"), r.URL.Query().Get("mode"), r.URL.Query().Get("date"))
	}
	if errors.Is(err, library.ErrPhotoCursor) {
		http.Error(w, `{"error":"invalid photo cursor"}`, http.StatusBadRequest)
		return
	}
	if errors.Is(err, library.ErrPhotoCursorStale) {
		http.Error(w, `{"error":"photo library changed; reload the page"}`, http.StatusConflict)
		return
	}
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) multiPhotoSearch(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	limit := library.PhotoPageDefault
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 {
			http.Error(w, `{"error":"invalid page limit"}`, http.StatusBadRequest)
			return
		}
	}
	page, err := res.Lib.PhotoSearchPage(r.Context(), library.PhotoSearchOptions{
		Camera: r.URL.Query().Get("camera"), OutsideAlbums: r.URL.Query().Get("outside_albums") == "1", Query: r.URL.Query().Get("q"), Album: r.URL.Query().Get("album"), Type: r.URL.Query().Get("type"),
		DateFrom: r.URL.Query().Get("from"), DateTo: r.URL.Query().Get("to"), Cursor: r.URL.Query().Get("cursor"),
		Favorite: r.URL.Query().Get("favorite") == "1", Hidden: r.URL.Query().Get("hidden") == "1", Archived: r.URL.Query().Get("archived") == "1", UnknownDates: r.URL.Query().Get("unknown") == "1", Limit: limit,
	})
	if errors.Is(err, library.ErrPhotoCursor) || errors.Is(err, library.ErrPhotoSearch) {
		http.Error(w, `{"error":"invalid photo search or cursor"}`, http.StatusBadRequest)
		return
	}
	if errors.Is(err, library.ErrPhotoCursorStale) {
		http.Error(w, `{"error":"photo search changed; reload results"}`, http.StatusConflict)
		return
	}
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) multiPhotoDates(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	summary, err := res.Lib.PhotoDateSummary(r.Context(), r.URL.Query().Get("hidden") == "1")
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) multiPhotoAsset(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/photos/assets/"), "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 {
		if parts[1] == "original" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			h.multiPhotoOriginal(w, r, parts[0])
			return
		}
		if parts[1] != "thumbnail" {
			http.NotFound(w, r)
			return
		}
		size := 320
		if raw := r.URL.Query().Get("size"); raw != "" {
			size, err = strconv.Atoi(raw)
			if err != nil || size < 96 || size > 1280 {
				http.Error(w, `{"error":"invalid preview size"}`, http.StatusBadRequest)
				return
			}
		}
		body, contentType, err := res.Lib.PhotoThumbnailVisible(r.Context(), parts[0], size, r.URL.Query().Get("hidden") == "1")
		if err != nil {
			if errors.Is(err, library.ErrThumbnailUnavailable) {
				http.NotFound(w, r)
				return
			}
			photoAPIError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Rotation    *int    `json:"rotation"`
			Favorite    *bool   `json:"favorite"`
			Archived    *bool   `json:"archived"`
			Caption     *string `json:"caption"`
			CapturedAt  *string `json:"captured_at"`
			OffsetKnown *bool   `json:"offset_known"`
		}
		if !decodeBody(w, r, &body, 8192) {
			return
		}
		update := library.PhotoUpdate{Rotation: body.Rotation, Favorite: body.Favorite, Archived: body.Archived, Caption: body.Caption}
		if body.CapturedAt != nil {
			if *body.CapturedAt == "" {
				update.Capture = &catalog.CaptureMetadata{Source: "user", UserCorrected: true}
			} else {
				captured, parseErr := time.Parse(time.RFC3339Nano, *body.CapturedAt)
				if parseErr != nil || captured.Year() < 1900 || captured.Year() > 2100 {
					http.Error(w, `{"error":"capture timestamp is invalid"}`, http.StatusBadRequest)
					return
				}
				_, offset := captured.Zone()
				offsetMinutes := offset / 60
				update.Capture = &catalog.CaptureMetadata{Time: &captured, OffsetMinutes: &offsetMinutes, Source: "user", UserCorrected: true}
				if body.OffsetKnown != nil && !*body.OffsetKnown {
					update.Capture.OffsetMinutes = nil
				}
			}
		}
		item, err := res.Lib.UpdatePhoto(r.Context(), parts[0], update, r.URL.Query().Get("hidden") == "1")
		if errors.Is(err, library.ErrPhotoCursorStale) {
			http.Error(w, `{"error":"photo asset changed; reload the page"}`, http.StatusConflict)
			return
		}
		if errors.Is(err, catalog.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			photoAPIError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, item)
		return
	}
	item, err := res.Lib.PhotoDetail(r.Context(), parts[0], r.URL.Query().Get("hidden") == "1")
	if errors.Is(err, library.ErrPhotoCursorStale) {
		http.Error(w, `{"error":"photo asset changed; reload the page"}`, http.StatusConflict)
		return
	}
	if errors.Is(err, catalog.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, item)
}
