package library

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type PhotoScope struct {
	Album  string             `json:"album,omitempty"`
	Mode   string             `json:"mode,omitempty"`
	Date   string             `json:"date,omitempty"`
	Search bool               `json:"search,omitempty"`
	Filter PhotoSearchOptions `json:"filter"`
}

func normalizePhotoScope(scope PhotoScope) (PhotoScope, *regexp.Regexp, string, error) {
	if scope.Mode == "" {
		scope.Mode = "all"
	}
	if scope.Mode != "all" && scope.Mode != "favorites" && scope.Mode != "archived" && scope.Mode != "hidden" {
		return scope, nil, "", ErrPhotoSearch
	}
	if !PhotoDateFilter(scope.Date) {
		return scope, nil, "", ErrPhotoSearch
	}
	scope.Filter.Cursor = ""
	scope.Filter.Limit = 0
	scope.Filter.Album = scope.Album
	scope.Filter.Hidden = scope.Mode == "hidden"
	scope.Filter.Favorite = scope.Mode == "favorites"
	scope.Filter.Archived = scope.Mode == "archived"
	options, pattern, err := normalizePhotoSearch(scope.Filter)
	if err != nil {
		return scope, nil, "", err
	}
	options.Limit = 0
	scope.Filter = options
	scope.Album = options.Album
	// Explicit date filter is part of the scope, never the seek destination.
	if scope.Date != "" {
		if scope.Date == "unknown" {
			scope.Filter.UnknownDates = true
		} else {
			from, to := scope.Date, scope.Date
			if len(from) == 4 {
				from += "-01-01"
				to += "-12-31"
			}
			if len(from) == 7 {
				from += "-01"
				to += "-31"
			}
			if scope.Filter.DateFrom == "" || scope.Filter.DateFrom < from {
				scope.Filter.DateFrom = from
			}
			if scope.Filter.DateTo == "" || scope.Filter.DateTo > to {
				scope.Filter.DateTo = to
			}
		}
	}
	raw, _ := json.Marshal(scope)
	return scope, pattern, string(raw), nil
}
func (l *Library) navigationViewLocked(scope PhotoScope, pattern *regexp.Regexp) (photoQueryView, error) {
	matches, members, revision, err := l.photoSearchMembers(scope.Filter)
	if err != nil {
		return photoQueryView{}, err
	}
	if scope.Search {
		return l.photoSearchQueryLocked(scope.Filter, matches, pattern, members, revision), nil
	}
	// Preserve ordinary timeline/album Archive semantics rather than pretending
	// that every timeline is a search with Archived=false.
	if scope.Album != "" && !strings.HasPrefix(scope.Album, "album:") {
		if !inPhotoRoot(scope.Album) {
			return photoQueryView{}, catalog.ErrAlbumNotFound
		}
	}
	return l.photoQueryLocked(scope.Album, scope.Mode, scope.Date, members, revision), nil
}
