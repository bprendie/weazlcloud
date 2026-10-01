package library

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/bprendie/weazlcloud/internal/vault"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type PhotoSearchOptions struct {
	Query         string `json:"q,omitempty"`
	Album         string `json:"album,omitempty"`
	Type          string `json:"type,omitempty"`
	DateFrom      string `json:"from,omitempty"`
	DateTo        string `json:"to,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	Camera        string `json:"camera,omitempty"`
	OutsideAlbums bool   `json:"outside_albums,omitempty"`
	Favorite      bool   `json:"favorite,omitempty"`
	Hidden        bool   `json:"hidden,omitempty"`
	Archived      bool   `json:"archived,omitempty"`
	UnknownDates  bool   `json:"unknown,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

var ErrPhotoSearch = errors.New("invalid photo search")

func (l *Library) PhotoSearchPage(ctx context.Context, options PhotoSearchOptions) (PhotoPage, error) {
	options.Query = strings.ToLower(strings.TrimSpace(options.Query))
	options.Camera = strings.ToLower(strings.TrimSpace(options.Camera))
	options.Type = strings.ToLower(strings.TrimSpace(options.Type))
	options.Album = strings.TrimSuffix(strings.TrimSpace(options.Album), "/")
	if len(options.Camera) > 120 || options.OutsideAlbums && options.Album != "" || len(options.Query) > 256 || len(options.Album) > 1024 || options.Type != "" && options.Type != "image" && options.Type != "video" || !validPhotoSearchDate(options.DateFrom) || !validPhotoSearchDate(options.DateTo) {
		return PhotoPage{}, ErrPhotoSearch
	}
	if options.DateFrom != "" && options.DateTo != "" && options.DateFrom > options.DateTo {
		return PhotoPage{}, ErrPhotoSearch
	}
	customAlbum := strings.HasPrefix(options.Album, "album:")
	if customAlbum {
		if len(strings.TrimPrefix(options.Album, "album:")) > 64 {
			return PhotoPage{}, ErrPhotoSearch
		}
	} else if options.Album != "" {
		clean, err := cleanPath(options.Album)
		if err != nil || !strings.HasPrefix(clean, PhotosRoot) {
			return PhotoPage{}, ErrPhotoSearch
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
			return PhotoPage{}, ErrPhotoSearch
		}
	}
	requestCursor := options.Cursor
	options.Cursor = ""
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoPage{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoPage{}, err
	}
	var albumMembers map[string]bool
	var albumRevision uint64
	searchOptions := options
	if customAlbum {
		searchOptions.Album = ""
		for _, album := range l.catalog.Albums() {
			if "album:"+album.ID != options.Album {
				continue
			}
			albumRevision = album.Revision
			albumMembers = make(map[string]bool, len(album.AssetIDs))
			for _, id := range album.AssetIDs {
				albumMembers[id] = true
			}
			break
		}
		if albumMembers == nil {
			return PhotoPage{}, catalog.ErrAlbumNotFound
		}
	}
	if options.OutsideAlbums {
		albumMembers = make(map[string]bool)
		for _, album := range l.catalog.Albums() {
			for _, id := range album.AssetIDs {
				albumMembers[id] = true
			}
		}
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	if l.photoSortedEpoch != l.photoEpoch {
		sortPhotoRows(l.photoMediaRows)
		l.reindexPhotoMediaLocked()
		l.photoSortedEpoch = l.photoEpoch
	}
	generation := l.photoEpoch
	view := l.photoSearchQueryLocked(options, searchOptions, pattern, albumMembers, albumRevision)
	start := 0
	if requestCursor != "" {
		cursor, err := l.decodePhotoCursor(requestCursor)
		if err != nil || cursor.Search != options.Query || cursor.Album != options.Album || cursor.SearchType != options.Type || cursor.DateFrom != options.DateFrom || cursor.DateTo != options.DateTo || cursor.Favorite != options.Favorite || cursor.Archived != options.Archived || cursor.UnknownDates != options.UnknownDates || cursor.Camera != options.Camera || cursor.OutsideAlbums != options.OutsideAlbums || (cursor.Mode == "hidden-search") != options.Hidden || cursor.Mode != "search" && cursor.Mode != "hidden-search" {
			return PhotoPage{}, ErrPhotoCursorStale
		}
		at := view.Index(cursor.AfterID)
		if at < 0 {
			start = view.Seek(cursor.Anchor, "all")
			if start < 0 {
				return PhotoPage{}, ErrPhotoCursorStale
			}
		} else {
			start = at + 1
		}
	}
	end := min(start+options.Limit, view.Len())
	page := PhotoPage{Items: make([]PhotoItem, 0, end-start), Generation: generation, IndexReady: l.photoReady, Indexed: len(l.photoRows), IndexTotal: len(l.photoRows)}
	for index := start; index < end; index++ {
		page.Items = append(page.Items, l.photoItemVisibleLocked(view.At(index), options.Hidden))
	}
	if end < view.Len() && end > start {
		mode := "search"
		if options.Hidden {
			mode = "hidden-search"
		}
		last := view.At(end - 1)
		var err error
		page.NextCursor, err = l.encodePhotoCursor(photoCursor{Generation: generation, AfterID: last.EntryID, Anchor: anchorOfPhoto(last), Album: options.Album, Mode: mode, Search: options.Query, SearchType: options.Type, DateFrom: options.DateFrom, DateTo: options.DateTo, Favorite: options.Favorite, Archived: options.Archived, UnknownDates: options.UnknownDates, Camera: options.Camera, OutsideAlbums: options.OutsideAlbums})
		if err != nil {
			return PhotoPage{}, err
		}
	}
	return page, nil
}

func photoItemFromFile(f catalog.File) PhotoItem {
	return PhotoItem{Components: append([]catalog.PhotoComponent(nil), f.PhotoComponents...), DeviceID: f.DeviceID, DeviceAssetID: f.DeviceAssetID, SourceRevision: f.SourceRevision, ID: f.EntryID, Revision: f.Revision, Folder: f.Folder, Path: f.Path, Size: f.Size, Modified: f.Mtime, ImportedAt: f.ImportedAt, CapturedAt: photoCaptureTime(f), CaptureOffsetMinutes: f.CaptureOffsetMinutes, CaptureSource: f.CaptureSource, MediaType: photoMediaType(f.Path), Width: f.Width, Height: f.Height, DurationMillis: f.DurationMillis, Orientation: f.Orientation, UserRotation: f.UserRotation, PreferredPhoto: f.PreferredPhoto, Camera: f.Camera, Favorite: f.Favorite, Archived: f.Archived, Caption: f.Caption}
}

func photoSearchMatches(file catalog.File, options PhotoSearchOptions, pattern *regexp.Regexp) bool {
	if options.Camera != "" && !strings.Contains(strings.ToLower(file.Camera), options.Camera) {
		return false
	}
	if options.OutsideAlbums && photoNamedFolderAlbum(file.Path) {
		return false
	}
	if options.Album != "" && !strings.HasPrefix(file.Path, options.Album+"/") {
		return false
	}
	if options.Favorite && !file.Favorite {
		return false
	}
	if options.Archived != file.Archived {
		return false
	}
	ext := strings.ToLower(path.Ext(file.Path))
	if options.Type == "image" && isPhotoVideo(file.Path) || options.Type == "video" && !isPhotoVideo(file.Path) {
		return false
	}
	if options.Type != "" && !strings.HasPrefix(photoMediaType(file.Path), options.Type+"/") {
		return false
	}
	if options.UnknownDates && file.CaptureTime != nil {
		return false
	}
	if file.CaptureTime != nil {
		date := photoCaptureDate(file, "2006-01-02")
		if options.DateFrom != "" && date < options.DateFrom || options.DateTo != "" && date > options.DateTo {
			return false
		}
	} else if options.DateFrom != "" || options.DateTo != "" {
		return false
	}
	if options.Query != "" {
		text := strings.ToLower(file.Path + " " + file.Caption + " " + strings.TrimPrefix(ext, "."))
		if pattern != nil && !pattern.MatchString(text) || pattern == nil && !strings.Contains(text, options.Query) {
			return false
		}
	}
	return true
}

func validPhotoSearchDate(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

// Requires photoMu. The bounded query cache stores row positions, not copies of
// every matching metadata record. Album revision participates in invalidation.
func (l *Library) photoSearchQueryLocked(options, matchOptions PhotoSearchOptions, pattern *regexp.Regexp, members map[string]bool, albumRevision uint64) photoQueryView {
	if l.photoQueryEpoch != l.photoEpoch || l.photoQueryCache == nil {
		l.photoQueryCache = make(map[string]photoQueryView)
		l.photoQueryOrder = nil
		l.photoQueryEpoch = l.photoEpoch
	}
	options.Cursor, options.Limit = "", 0
	raw, _ := json.Marshal(struct {
		Options  PhotoSearchOptions
		Revision uint64
	}{options, albumRevision})
	key := "search:" + string(raw)
	if view, ok := l.photoQueryCache[key]; ok {
		return view
	}
	view := photoQueryView{Rows: l.photoMediaRows, Indices: []int{}, ByID: make(map[string]int)}
	for index, file := range l.photoMediaRows {
		if members != nil && ((!options.OutsideAlbums && !members[file.EntryID]) || (options.OutsideAlbums && members[file.EntryID])) || l.photoPathHiddenLocked(file.Path) != options.Hidden || !photoSearchMatches(file, matchOptions, pattern) {
			continue
		}
		view.ByID[file.EntryID] = len(view.Indices)
		view.Indices = append(view.Indices, index)
	}
	if len(l.photoQueryOrder) >= 16 {
		delete(l.photoQueryCache, l.photoQueryOrder[0])
		l.photoQueryOrder = l.photoQueryOrder[1:]
	}
	l.photoQueryOrder = append(l.photoQueryOrder, key)
	l.photoQueryCache[key] = view
	return view
}

func photoNamedFolderAlbum(name string) bool {
	rel := strings.TrimPrefix(name, PhotosRoot)
	folder, _, nested := strings.Cut(rel, "/")
	return nested && !photoYearFolder.MatchString(folder) && !strings.EqualFold(folder, "Trash") && !strings.EqualFold(folder, "Failed Videos")
}
