package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
	"strings"
)

// PhotoPage returns a bounded page from an owner-private in-memory index.
// The first request reads catalog metadata once; it never restores photo bytes.
func (l *Library) PhotoPage(ctx context.Context, limit int, cursor, album string) (PhotoPage, error) {
	return l.PhotoTimelinePage(ctx, limit, cursor, album, "all", "")
}

// PhotoTimelinePage returns a bounded owner-private page for a timeline mode.
// Cursors bind both album and filters so they cannot be replayed across views.
func (l *Library) PhotoTimelinePage(ctx context.Context, limit int, cursor, album, mode, date string) (PhotoPage, error) {
	return l.photoTimelinePage(ctx, limit, cursor, album, mode, date, "")
}

// PhotoTimelinePageAround returns a bounded page centered on a stable asset
// ID and exposes backward/forward cursors around that anchor.
func (l *Library) PhotoTimelinePageAround(ctx context.Context, limit int, album, mode, date, anchorID string) (PhotoPage, error) {
	if anchorID == "" || len(anchorID) > 128 {
		return PhotoPage{}, ErrPhotoCursor
	}
	return l.photoTimelinePage(ctx, limit, "", album, mode, date, anchorID)
}

func (l *Library) photoTimelinePage(ctx context.Context, limit int, cursor, album, mode, date, aroundID string) (PhotoPage, error) {
	if limit <= 0 {
		limit = PhotoPageDefault
	}
	if limit > PhotoPageMaximum {
		limit = PhotoPageMaximum
	}
	var albumMembers map[string]bool
	var albumRevision uint64
	if strings.HasPrefix(album, "album:") {
		if len(strings.TrimPrefix(album, "album:")) > 64 {
			return PhotoPage{}, ErrPhotoCursor
		}
	} else if album != "" {
		clean, err := cleanPath(album)
		if err != nil || !strings.HasPrefix(clean, PhotosRoot) {
			return PhotoPage{}, ErrPhotoCursor
		}
		album = clean
	}
	if mode == "" {
		mode = "all"
	}
	if mode != "all" && mode != "favorites" && mode != "recent" && mode != "hidden" && mode != "archived" {
		return PhotoPage{}, ErrPhotoCursor
	}
	if !PhotoDateFilter(date) {
		return PhotoPage{}, ErrPhotoCursor
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoPage{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoPage{}, err
	}
	if strings.HasPrefix(album, "album:") {
		id := strings.TrimPrefix(album, "album:")
		for _, saved := range l.catalog.Albums() {
			if saved.ID == id {
				albumRevision = saved.Revision
				albumMembers = make(map[string]bool, len(saved.AssetIDs))
				for _, entryID := range saved.AssetIDs {
					albumMembers[entryID] = true
				}
				break
			}
		}
		if albumMembers == nil {
			return PhotoPage{}, catalog.ErrAlbumNotFound
		}
	}
	var afterID, beforeID string
	var cursorAnchor *photoAnchor
	if cursor != "" {
		decoded, err := l.decodePhotoCursor(cursor)
		if err != nil {
			return PhotoPage{}, err
		}
		if decoded.Album != album || decoded.Mode != mode || decoded.Date != date {
			return PhotoPage{}, ErrPhotoCursorStale
		}
		afterID = decoded.AfterID
		beforeID = decoded.BeforeID
		cursorAnchor = decoded.Anchor
		if (afterID == "") == (beforeID == "") {
			return PhotoPage{}, ErrPhotoCursor
		}
	}
	l.photoMu.Lock()
	if l.photoSortedEpoch != l.photoEpoch {
		sortPhotoRows(l.photoMediaRows)
		l.reindexPhotoMediaLocked()
		l.photoSortedEpoch = l.photoEpoch
	}
	generation := l.photoEpoch
	indexed := len(l.photoRows)
	indexReady := l.photoReady
	filtered := l.photoQueryLocked(album, mode, date, albumMembers, albumRevision)
	start, end := 0, filtered.Len()
	if afterID != "" {
		anchor := filtered.Index(afterID)
		if anchor < 0 {
			start = filtered.Seek(cursorAnchor, mode)
			if start < 0 {
				l.photoMu.Unlock()
				return PhotoPage{}, ErrPhotoCursorStale
			}
		} else {
			start = anchor + 1
		}
	}
	if beforeID != "" {
		anchor := filtered.Index(beforeID)
		if anchor < 0 {
			anchor = filtered.Seek(cursorAnchor, mode)
			if anchor < 0 {
				l.photoMu.Unlock()
				return PhotoPage{}, ErrPhotoCursorStale
			}
		}
		end = anchor
		start = max(0, end-limit)
	}
	if aroundID != "" {
		anchor := filtered.Index(aroundID)
		if anchor < 0 {
			all := l.catalog.All()
			folders := photoTrashHiddenFolders(all)
			for _, file := range all {
				if file.EntryID == aroundID && !file.Present && !file.Folder && photoMedia(file.Path) && photoPathHidden(file.Path, folders) == (mode == "hidden") {
					anchor = filtered.Seek(anchorOfPhoto(file), mode)
					break
				}
			}
		}
		if anchor < 0 {
			l.photoMu.Unlock()
			return PhotoPage{}, ErrPhotoCursorStale
		}
		start = max(0, anchor-limit/2)
		end = min(start+limit, filtered.Len())
		start = max(0, end-limit)
	}
	if beforeID == "" && aroundID == "" {
		end = min(start+limit, filtered.Len())
	}
	pageFiles := make([]catalog.File, 0, end-start)
	for index := start; index < end; index++ {
		pageFiles = append(pageFiles, filtered.At(index))
	}
	hasPrevious := start > 0
	hasNext := end < filtered.Len()
	l.photoMu.Unlock()
	page := PhotoPage{Items: make([]PhotoItem, 0, len(pageFiles)), Generation: generation, IndexReady: indexReady, Indexed: indexed, IndexTotal: indexed}
	for _, f := range pageFiles {
		page.Items = append(page.Items, l.photoItemVisible(f, mode == "hidden"))
	}
	if len(pageFiles) != 0 {
		var err error
		if hasNext && beforeID == "" {
			page.NextCursor, err = l.encodePhotoCursor(photoCursor{Generation: generation, AfterID: pageFiles[len(pageFiles)-1].EntryID, Album: album, Mode: mode, Date: date, Anchor: anchorOfPhoto(pageFiles[len(pageFiles)-1])})
			if err != nil {
				return PhotoPage{}, err
			}
		}
		if hasPrevious && afterID == "" {
			page.PreviousCursor, err = l.encodePhotoCursor(photoCursor{Generation: generation, BeforeID: pageFiles[0].EntryID, Album: album, Mode: mode, Date: date, Anchor: anchorOfPhoto(pageFiles[0])})
			if err != nil {
				return PhotoPage{}, err
			}
		}
	}
	l.resumePhotoPreparation()
	return page, nil
}

func photoIndexOfID(files []catalog.File, id string) int {
	for index := range files {
		if files[index].EntryID == id {
			return index
		}
	}
	return -1
}
