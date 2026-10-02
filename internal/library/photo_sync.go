package library

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type photoSyncCursor struct {
	Position   catalog.SyncPosition `json:"position"`
	Mode       string               `json:"mode"`
	After      string               `json:"after,omitempty"`
	AlbumAfter string               `json:"album_after,omitempty"`
	Hidden     bool                 `json:"hidden,omitempty"`
}

type PhotoSyncChange struct {
	Sequence          uint64      `json:"sequence"`
	Kind              string      `json:"kind"`
	ID                string      `json:"id"`
	Deleted           bool        `json:"deleted,omitempty"`
	Item              *PhotoItem  `json:"item,omitempty"`
	Album             *PhotoAlbum `json:"album,omitempty"`
	MembershipChanged bool        `json:"membership_changed,omitempty"`
}

type PhotoSyncPage struct {
	Items          []PhotoItem       `json:"items"`
	Albums         []PhotoAlbum      `json:"albums,omitempty"`
	Changes        []PhotoSyncChange `json:"changes,omitempty"`
	NextCursor     string            `json:"next_cursor,omitempty"`
	Checkpoint     string            `json:"checkpoint,omitempty"`
	HasMore        bool              `json:"has_more"`
	ResyncRequired bool              `json:"resync_required"`
}

// Initial snapshots are stable-ID paged. Their final checkpoint precedes the
// snapshot: replaying subsequent deltas reconciles concurrent edits and deletes.
func (l *Library) PhotoSync(ctx context.Context, raw string, limit int, hidden bool) (PhotoSyncPage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoSyncPage{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoSyncPage{}, err
	}
	limit = max(1, min(limit, PhotoPageMaximum))
	var cursor photoSyncCursor
	if raw == "" {
		position, err := l.catalog.SyncPosition()
		if err != nil {
			return PhotoSyncPage{}, err
		}
		cursor = photoSyncCursor{Position: position, Mode: "snapshot", Hidden: hidden}
	} else {
		var err error
		cursor, err = l.decodeSyncCursor(raw)
		if err != nil || cursor.Hidden != hidden {
			return PhotoSyncPage{}, catalog.ErrSyncExpired
		}
		if err = l.catalog.ValidateSync(cursor.Position); err != nil {
			return PhotoSyncPage{}, err
		}
	}
	if cursor.Mode == "delta" {
		return l.photoSyncDeltaLocked(cursor, limit)
	}
	if cursor.Mode == "albums" {
		return l.photoSyncAlbumsLocked(cursor, limit)
	}
	l.photoMu.Lock()
	view := l.photoSyncViewLocked(hidden)
	start := sort.Search(view.Len(), func(index int) bool { return view.At(index).EntryID > cursor.After })
	page := PhotoSyncPage{Items: make([]PhotoItem, 0, limit)}
	for index := start; index < view.Len(); index++ {
		file := view.At(index)
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, l.photoItemVisibleLocked(file, hidden))
	}
	l.photoMu.Unlock()
	var encodeErr error
	if page.HasMore {
		cursor.After = page.Items[len(page.Items)-1].ID
		page.NextCursor, encodeErr = l.encodeSyncCursor(cursor)
	} else {
		cursor.Mode, cursor.After = "albums", ""
		if len(l.catalog.Albums()) == 0 {
			cursor.Mode = "delta"
			page.Checkpoint, encodeErr = l.encodeSyncCursor(cursor)
		} else {
			page.HasMore = true
			page.NextCursor, encodeErr = l.encodeSyncCursor(cursor)
		}
	}
	return page, encodeErr
}

func (l *Library) photoSyncDeltaLocked(cursor photoSyncCursor, limit int) (PhotoSyncPage, error) {
	records, next, more, err := l.catalog.ChangesAfter(cursor.Position, limit)
	if err != nil {
		return PhotoSyncPage{}, err
	}
	page := PhotoSyncPage{Items: []PhotoItem{}, HasMore: more}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	for _, record := range records {
		if !record.Photos || record.Kind == "collection" || record.Kind == "source-mapping" {
			continue
		}
		change := PhotoSyncChange{Sequence: record.Sequence, Kind: record.Kind, ID: record.ID, Deleted: record.Deleted}
		if record.Kind == "folder" {
			page.ResyncRequired = true
		}
		if record.File != nil && !change.Deleted {
			file := *record.File
			if !inPhotoRoot(file.Path) || l.photoPathHiddenLocked(file.Path) != cursor.Hidden || !file.Folder && !photoMedia(file.Path) {
				change.Deleted = true
			} else {
				item := l.photoItemVisibleLocked(file, cursor.Hidden)
				change.Item = &item
			}
		}
		if record.Album != nil && !change.Deleted {
			album := l.syncAlbumLocked(*record.Album, cursor.Hidden)
			album.AssetIDs = nil
			change.Album = &album
			change.MembershipChanged = true
		}
		page.Changes = append(page.Changes, change)
	}
	cursor.Position = next
	if more {
		page.NextCursor, err = l.encodeSyncCursor(cursor)
	} else {
		page.Checkpoint, err = l.encodeSyncCursor(cursor)
	}
	return page, err
}

// Requires photoMu. Membership stays private and complete in the catalog;
// presentation excludes unavailable and out-of-context members.
func (l *Library) syncAlbumLocked(album catalog.Album, hidden bool) PhotoAlbum {
	view := PhotoAlbum{ID: album.ID, ParentID: album.ParentID, Revision: album.Revision, Path: "album:" + album.ID, Title: album.Title, Description: album.Description, Position: album.Position, Source: "custom", AssetIDs: []string{}}
	for _, id := range album.AssetIDs {
		file, ok := l.photoByID[id]
		if !ok || file.Folder || file.PhotoParentID != "" || !photoMedia(file.Path) || l.photoPathHiddenLocked(file.Path) != hidden {
			continue
		}
		view.AssetIDs = append(view.AssetIDs, id)
		if id == album.CoverID || view.CoverID == "" {
			view.CoverID = id
		}
	}
	view.Count = len(view.AssetIDs)
	return view
}

func (l *Library) encodeSyncCursor(cursor photoSyncCursor) (string, error) {
	plain, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	defer clear(plain)
	raw, err := l.vault.Wrap(plain)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (l *Library) decodeSyncCursor(raw string) (photoSyncCursor, error) {
	var cursor photoSyncCursor
	if len(raw) > 4096 {
		return cursor, catalog.ErrSyncExpired
	}
	wrapped, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, catalog.ErrSyncExpired
	}
	plain, err := l.vault.Unwrap(wrapped)
	if err != nil {
		return cursor, catalog.ErrSyncExpired
	}
	defer clear(plain)
	if json.Unmarshal(plain, &cursor) != nil || cursor.Position.Epoch == "" || len(cursor.After) > 128 || cursor.Mode != "snapshot" && cursor.Mode != "albums" && cursor.Mode != "delta" {
		return cursor, errors.New("invalid sync cursor")
	}
	return cursor, nil
}
