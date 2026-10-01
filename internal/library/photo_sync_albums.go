package library

import (
	"context"
	"sort"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Sync transfers album headers separately from memberships. Clients reconcile
// membership_changed records through this bounded, revision-bound endpoint.
func (l *Library) photoSyncAlbumsLocked(cursor photoSyncCursor, limit int) (PhotoSyncPage, error) {
	albums := l.catalog.Albums()
	sort.Slice(albums, func(i, j int) bool { return albums[i].ID < albums[j].ID })
	start := sort.Search(len(albums), func(i int) bool { return albums[i].ID > cursor.AlbumAfter })
	end := min(start+limit, len(albums))
	page := PhotoSyncPage{Items: []PhotoItem{}, HasMore: end < len(albums)}
	l.photoMu.Lock()
	for _, album := range albums[start:end] {
		view := l.syncAlbumLocked(album, cursor.Hidden)
		view.AssetIDs = nil
		page.Albums = append(page.Albums, view)
	}
	l.photoMu.Unlock()
	var err error
	if page.HasMore {
		cursor.AlbumAfter = albums[end-1].ID
		page.NextCursor, err = l.encodeSyncCursor(cursor)
	} else {
		cursor.Mode, cursor.AlbumAfter = "delta", ""
		page.Checkpoint, err = l.encodeSyncCursor(cursor)
	}
	return page, err
}

type PhotoAlbumMembershipPage struct {
	ID         string   `json:"id"`
	Revision   uint64   `json:"revision"`
	AssetIDs   []string `json:"asset_ids"`
	NextCursor string   `json:"next_cursor,omitempty"`
	HasMore    bool     `json:"has_more"`
}

func (l *Library) PhotoAlbumMemberships(ctx context.Context, id, raw string, limit int, hidden bool) (PhotoAlbumMembershipPage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoAlbumMembershipPage{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoAlbumMembershipPage{}, err
	}
	var album catalog.Album
	for _, candidate := range l.catalog.Albums() {
		if candidate.ID == id {
			album = candidate
			break
		}
	}
	if album.ID == "" {
		return PhotoAlbumMembershipPage{}, catalog.ErrAlbumNotFound
	}
	limit = max(1, min(limit, PhotoPageMaximum))
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	view := l.syncAlbumLocked(album, hidden)
	sort.Strings(view.AssetIDs)
	start := 0
	if raw != "" {
		cursor, err := l.decodePhotoCursor(raw)
		if err != nil || cursor.Mode != "membership" || cursor.Album != id || cursor.Generation != l.photoEpoch || cursor.Favorite != hidden {
			return PhotoAlbumMembershipPage{}, ErrPhotoCursorStale
		}
		start = sort.SearchStrings(view.AssetIDs, cursor.AfterID)
		if start < len(view.AssetIDs) && view.AssetIDs[start] == cursor.AfterID {
			start++
		}
	}
	end := min(start+limit, len(view.AssetIDs))
	page := PhotoAlbumMembershipPage{ID: id, Revision: album.Revision, AssetIDs: append([]string{}, view.AssetIDs[start:end]...), HasMore: end < len(view.AssetIDs)}
	if page.HasMore {
		var err error
		page.NextCursor, err = l.encodePhotoCursor(photoCursor{Mode: "membership", Album: id, Generation: l.photoEpoch, Favorite: hidden, AfterID: view.AssetIDs[end-1]})
		if err != nil {
			return PhotoAlbumMembershipPage{}, err
		}
	}
	return page, nil
}
