package desk

import (
	"net/http"
	"strconv"

	"github.com/bprendie/weazlcloud/internal/library"
)

func photoRequestScope(r *http.Request) library.PhotoScope {
	q := r.URL.Query()
	mode := q.Get("mode")
	if q.Get("hidden") == "1" {
		mode = "hidden"
	}
	if mode == "" && q.Get("favorite") == "1" {
		mode = "favorites"
	}
	if mode == "" && q.Get("archived") == "1" {
		mode = "archived"
	}
	filter := library.PhotoSearchOptions{Query: q.Get("q"), Camera: q.Get("camera"), Type: q.Get("type"), DateFrom: q.Get("from"), DateTo: q.Get("to"), OutsideAlbums: q.Get("outside_albums") == "1", UnknownDates: q.Get("unknown") == "1"}
	search := q.Get("search") == "1" || filter.Query != "" || filter.Camera != "" || filter.Type != "" || filter.DateFrom != "" || filter.DateTo != "" || filter.OutsideAlbums || filter.UnknownDates
	return library.PhotoScope{Album: q.Get("album"), Mode: mode, Date: q.Get("date"), Search: search, Filter: filter}
}
func (h *Handler) photoNavigation(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	request := library.PhotoNavigationRequest{Scope: photoRequestScope(r), At: r.URL.Query().Get("at"), Around: r.URL.Query().Get("around"), Cursor: r.URL.Query().Get("cursor")}
	for _, name := range []string{"rank", "limit"} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, parseErr := strconv.Atoi(raw)
			if parseErr != nil || value < 0 || name == "limit" && value == 0 {
				http.Error(w, `{"error":"invalid navigation position or limit"}`, 400)
				return
			}
			if name == "rank" {
				request.Rank = &value
			} else {
				request.Limit = value
			}
		}
	}
	page, err := res.Lib.PhotoNavigation(r.Context(), request)
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, page)
}
func (h *Handler) photoNavigationDates(w http.ResponseWriter, r *http.Request) {
	res, _, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	summary, err := res.Lib.PhotoNavigationSummary(r.Context(), photoRequestScope(r), r.URL.Query().Get("month"))
	if err != nil {
		photoAPIError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, summary)
}
