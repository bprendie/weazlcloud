package library

import (
	"github.com/bprendie/weazlcloud/internal/catalog"
	"regexp"
	"strings"
)

func normalizePhotoSearch(options PhotoSearchOptions) (PhotoSearchOptions, *regexp.Regexp, error) {
	options.Query = strings.ToLower(strings.TrimSpace(options.Query))
	options.Camera = strings.ToLower(strings.TrimSpace(options.Camera))
	options.Type = strings.ToLower(strings.TrimSpace(options.Type))
	options.Album = strings.TrimSuffix(strings.TrimSpace(options.Album), "/")
	if len(options.Camera) > 120 || options.OutsideAlbums && options.Album != "" || len(options.Query) > 256 || len(options.Album) > 1024 || options.Type != "" && options.Type != "image" && options.Type != "video" || !validPhotoSearchDate(options.DateFrom) || !validPhotoSearchDate(options.DateTo) {
		return options, nil, ErrPhotoSearch
	}
	if options.DateFrom != "" && options.DateTo != "" && options.DateFrom > options.DateTo {
		return options, nil, ErrPhotoSearch
	}
	customAlbum := strings.HasPrefix(options.Album, "album:")
	if customAlbum {
		if len(strings.TrimPrefix(options.Album, "album:")) > 64 {
			return options, nil, ErrPhotoSearch
		}
	} else if options.Album != "" {
		clean, err := cleanPath(options.Album)
		if err != nil || !strings.HasPrefix(clean, PhotosRoot) {
			return options, nil, ErrPhotoSearch
		}
		options.Album = clean
	}
	if options.Limit <= 0 {
		options.Limit = PhotoPageDefault
	}
	if options.Limit > PhotoPageMaximum {
		options.Limit = PhotoPageMaximum
	}
	var pattern *regexp.Regexp
	if strings.ContainsAny(options.Query, "*?") {
		quoted := regexp.QuoteMeta(options.Query)
		quoted = strings.ReplaceAll(strings.ReplaceAll(quoted, `\*`, ".*"), `\?`, ".")
		var err error
		pattern, err = regexp.Compile(quoted)
		if err != nil {
			return options, nil, ErrPhotoSearch
		}
	}

	return options, pattern, nil
}

// The caller holds Library.mu. Membership validation is shared by scoped
// navigation and the existing search predicate/cache.
func (l *Library) photoSearchMembers(options PhotoSearchOptions) (PhotoSearchOptions, map[string]bool, uint64, error) {
	matches := options
	var members map[string]bool
	var revision uint64
	if strings.HasPrefix(options.Album, "album:") {
		matches.Album = ""
		for _, album := range l.catalog.Albums() {
			if "album:"+album.ID == options.Album {
				revision = album.Revision
				members = map[string]bool{}
				for _, id := range album.AssetIDs {
					members[id] = true
				}
				break
			}
		}
		if members == nil {
			return matches, nil, 0, catalog.ErrAlbumNotFound
		}
	}
	if options.OutsideAlbums {
		members = map[string]bool{}
		for _, album := range l.catalog.Albums() {
			for _, id := range album.AssetIDs {
				members[id] = true
			}
		}
	}
	return matches, members, revision, nil
}
