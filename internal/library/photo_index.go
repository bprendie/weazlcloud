package library

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
	"strings"
	"time"
)

const (
	PhotoPageDefault = 100
	PhotoPageMaximum = 200
)

var (
	ErrPhotoCursor      = errors.New("invalid photo cursor")
	ErrPhotoCursorStale = errors.New("photo library changed; reload the page")
)

type PhotoItem struct {
	PreviewUnsupported   bool                     `json:"preview_unsupported,omitempty"`
	PreviewIdentity      string                   `json:"preview_identity,omitempty"`
	ThumbHash            []byte                   `json:"thumbhash,omitempty"`
	ParentAssetID        string                   `json:"parent_asset_id,omitempty"`
	Components           []catalog.PhotoComponent `json:"components,omitempty"`
	DeviceID             string                   `json:"device_id,omitempty"`
	DeviceAssetID        string                   `json:"device_asset_id,omitempty"`
	SourceRevision       string                   `json:"source_revision,omitempty"`
	ID                   string                   `json:"id"`
	Revision             uint64                   `json:"revision"`
	Folder               bool                     `json:"folder,omitempty"`
	Path                 string                   `json:"path"`
	Size                 int64                    `json:"size"`
	Modified             time.Time                `json:"modified"`
	ImportedAt           time.Time                `json:"imported_at,omitempty"`
	CapturedAt           *time.Time               `json:"captured_at,omitempty"`
	CaptureOffsetMinutes *int                     `json:"capture_offset_minutes,omitempty"`
	CaptureSource        string                   `json:"capture_source,omitempty"`
	MediaType            string                   `json:"media_type"`
	Width                int                      `json:"width,omitempty"`
	Height               int                      `json:"height,omitempty"`
	DurationMillis       int64                    `json:"duration_millis,omitempty"`
	Orientation          int                      `json:"orientation,omitempty"`
	PreferredPhoto       bool                     `json:"preferred_photo,omitempty"`
	Camera               string                   `json:"camera,omitempty"`
	UserRotation         int                      `json:"user_rotation,omitempty"`
	Favorite             bool                     `json:"favorite,omitempty"`
	Archived             bool                     `json:"archived,omitempty"`
	Caption              string                   `json:"caption,omitempty"`
}

type PhotoPage struct {
	Items          []PhotoItem `json:"items"`
	NextCursor     string      `json:"next_cursor,omitempty"`
	PreviousCursor string      `json:"previous_cursor,omitempty"`
	Generation     uint64      `json:"generation"`
	IndexReady     bool        `json:"index_ready"`
	Indexed        int         `json:"indexed"`
	IndexTotal     int         `json:"index_total"`
}

// PhotoDetail resolves a current media identity from the owner's private photo
// index. It never returns a path outside the Photos collection.
func (l *Library) PhotoDetail(ctx context.Context, entryID string, hiddenView ...bool) (PhotoItem, error) {
	if entryID == "" || len(entryID) > 128 {
		return PhotoItem{}, ErrPhotoCursor
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoItem{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoItem{}, err
	}
	l.photoMu.Lock()
	file, ok := l.photoByID[entryID]
	l.photoMu.Unlock()
	if !ok || file.Folder || !photoMedia(file.Path) || !strings.HasPrefix(file.Path, PhotosRoot) {
		return PhotoItem{}, catalog.ErrNotFound
	}
	l.photoMu.Lock()
	hidden := l.photoPathHiddenLocked(file.Path)
	l.photoMu.Unlock()
	wantHidden := len(hiddenView) != 0 && hiddenView[0]
	if hidden != wantHidden {
		return PhotoItem{}, catalog.ErrNotFound
	}
	current, ok := l.catalog.Get(file.Path)
	if !ok || current.EntryID != file.EntryID || current.Revision != file.Revision {
		return PhotoItem{}, ErrPhotoCursorStale
	}
	return l.photoItemVisible(current, wantHidden), nil
}

// SetPhotoFolderHidden persists recursive Photos visibility on a stable folder.
func (l *Library) SetPhotoFolderHidden(ctx context.Context, name string, hidden bool) (catalog.File, error) {
	name, err := cleanPath(name)
	if err != nil || !inPhotoRoot(name) {
		return catalog.File{}, catalog.ErrNotFound
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return catalog.File{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return catalog.File{}, err
	}
	file, ok := l.catalog.Get(name)
	// Older imports and native upload paths can have implicit folders. Give
	// such a folder stable encrypted identity before attaching visibility.
	if !ok && l.catalog.IsFolder(name) {
		if err := l.catalog.Mkdir(name); err != nil {
			return catalog.File{}, err
		}
		file, ok = l.catalog.Get(name)
	}
	if !ok || !file.Folder {
		return catalog.File{}, catalog.ErrNotFound
	}
	updated, err := l.catalog.SetFolderHidden(file.EntryID, file.Revision, hidden)
	if err != nil {
		return catalog.File{}, err
	}
	l.publishChange(Change{Kind: "photo-visibility", Paths: []string{name}})
	return updated, nil
}

// SetPhotoFavorite persists the owner's Photos favorite flag in the encrypted
// catalog and publishes a change so open timelines can refresh.
func (l *Library) SetPhotoFavorite(ctx context.Context, entryID string, favorite bool, hiddenView ...bool) (PhotoItem, error) {
	if entryID == "" || len(entryID) > 128 {
		return PhotoItem{}, ErrPhotoCursor
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoItem{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoItem{}, err
	}
	l.photoMu.Lock()
	indexed, ok := l.photoByID[entryID]
	l.photoMu.Unlock()
	if !ok || indexed.Folder || !photoMedia(indexed.Path) || !strings.HasPrefix(indexed.Path, PhotosRoot) {
		return PhotoItem{}, catalog.ErrNotFound
	}
	l.photoMu.Lock()
	hidden := l.photoPathHiddenLocked(indexed.Path)
	l.photoMu.Unlock()
	wantHidden := len(hiddenView) != 0 && hiddenView[0]
	if hidden != wantHidden {
		return PhotoItem{}, catalog.ErrNotFound
	}
	current, ok := l.catalog.Get(indexed.Path)
	if !ok || current.EntryID != indexed.EntryID || current.Revision != indexed.Revision {
		return PhotoItem{}, ErrPhotoCursorStale
	}
	updated, err := l.catalog.UpdateMedia(current.EntryID, current.Revision, catalog.MediaMetadata{
		Width: current.Width, Height: current.Height, DurationMillis: current.DurationMillis,
		Orientation: current.Orientation, Favorite: favorite, Archived: current.Archived,
		Caption: current.Caption, Camera: current.Camera, UserRotation: current.UserRotation,
	})
	if err != nil {
		return PhotoItem{}, err
	}
	l.publishChange(Change{Kind: "photo-metadata", Paths: []string{updated.Path}})
	return l.photoItemVisible(updated, false), nil
}

type photoCursor struct {
	Scope         string       `json:"scope,omitempty"`
	Generation    uint64       `json:"generation"`
	AfterID       string       `json:"after_id,omitempty"`
	BeforeID      string       `json:"before_id,omitempty"`
	Album         string       `json:"album,omitempty"`
	Mode          string       `json:"mode,omitempty"`
	Date          string       `json:"date,omitempty"`
	Search        string       `json:"search,omitempty"`
	SearchType    string       `json:"search_type,omitempty"`
	DateFrom      string       `json:"date_from,omitempty"`
	DateTo        string       `json:"date_to,omitempty"`
	Favorite      bool         `json:"favorite,omitempty"`
	OutsideAlbums bool         `json:"outside_albums,omitempty"`
	Camera        string       `json:"camera,omitempty"`
	UnknownDates  bool         `json:"unknown_dates,omitempty"`
	Archived      bool         `json:"archived,omitempty"`
	Anchor        *photoAnchor `json:"anchor,omitempty"`
}
