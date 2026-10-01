package library

import (
	"context"
	"encoding/hex"
	"sort"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type PhotoDuplicateGroup struct {
	ID           string      `json:"id"`
	Count        int         `json:"count"`
	Items        []PhotoItem `json:"items"`
	HasMoreItems bool        `json:"has_more_items"`
}
type PhotoDuplicatePage struct {
	Groups     []PhotoDuplicateGroup `json:"groups"`
	NextCursor string                `json:"next_cursor,omitempty"`
	Generation uint64                `json:"generation"`
}

func (l *Library) PhotoDuplicates(ctx context.Context, raw string, limit int, hidden bool) (PhotoDuplicatePage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoDuplicatePage{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoDuplicatePage{}, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	limit = max(1, min(limit, 50))
	view, err := l.photoDuplicateViewLocked(hidden)
	if err != nil {
		return PhotoDuplicatePage{}, err
	}
	keys := make([]string, 0, len(view.Groups))
	for id := range view.Groups {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	start := 0
	mode := "duplicates"
	if hidden {
		mode = "hidden-duplicates"
	}
	if raw != "" {
		cursor, err := l.decodePhotoCursor(raw)
		if err != nil || cursor.Generation != l.photoEpoch || cursor.Mode != mode {
			return PhotoDuplicatePage{}, ErrPhotoCursorStale
		}
		start = sort.SearchStrings(keys, cursor.AfterID)
		if start == len(keys) || keys[start] != cursor.AfterID {
			return PhotoDuplicatePage{}, ErrPhotoCursorStale
		}
		start++
	}
	page := PhotoDuplicatePage{Groups: []PhotoDuplicateGroup{}, Generation: l.photoEpoch}
	end := min(start+limit, len(keys))
	for _, id := range keys[start:end] {
		positions := view.Groups[id]
		group := PhotoDuplicateGroup{ID: id, Count: len(positions), Items: []PhotoItem{}, HasMoreItems: len(positions) > 200}
		for _, pos := range positions[:min(len(positions), 200)] {
			group.Items = append(group.Items, l.photoItemVisibleLocked(view.Rows[pos], hidden))
		}
		page.Groups = append(page.Groups, group)
	}
	if end < len(keys) {
		page.NextCursor, err = l.encodePhotoCursor(photoCursor{Generation: l.photoEpoch, Mode: mode, AfterID: keys[end-1]})
	}
	return page, err
}

func (l *Library) photoDuplicateViewLocked(hidden bool) (photoQueryView, error) {
	if l.photoQueryEpoch != l.photoEpoch || l.photoQueryCache == nil {
		l.photoQueryCache = make(map[string]photoQueryView)
		l.photoQueryOrder = nil
		l.photoQueryEpoch = l.photoEpoch
	}
	key := "duplicates:normal"
	if hidden {
		key = "duplicates:hidden"
	}
	if view, ok := l.photoQueryCache[key]; ok {
		return view, nil
	}
	byHash := make(map[string][]int)
	for pos, file := range l.photoMediaRows {
		if file.Hash != "" && l.photoPathHiddenLocked(file.Path) == hidden {
			byHash[file.Hash] = append(byHash[file.Hash], pos)
		}
	}
	view := photoQueryView{Rows: l.photoMediaRows, Groups: make(map[string][]int)}
	for hash, positions := range byHash {
		if len(positions) < 2 {
			continue
		}
		id, err := l.vault.Fingerprint("photo-duplicate", []byte(hash))
		if err != nil {
			return view, err
		}
		sort.SliceStable(positions, func(i, j int) bool {
			left, right := view.Rows[positions[i]], view.Rows[positions[j]]
			if left.PreferredPhoto != right.PreferredPhoto {
				return left.PreferredPhoto
			}
			return left.EntryID < right.EntryID
		})
		view.Groups[hex.EncodeToString(id)] = positions
	}
	if len(l.photoQueryOrder) >= 16 {
		delete(l.photoQueryCache, l.photoQueryOrder[0])
		l.photoQueryOrder = l.photoQueryOrder[1:]
	}
	l.photoQueryOrder = append(l.photoQueryOrder, key)
	l.photoQueryCache[key] = view
	return view, nil
}

func (l *Library) PreferPhoto(ctx context.Context, id string, hidden bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return err
	}
	l.photoMu.Lock()
	file, ok := l.photoByID[id]
	if !ok || file.Folder || file.Hash == "" || l.photoPathHiddenLocked(file.Path) != hidden {
		l.photoMu.Unlock()
		return catalog.ErrNotFound
	}
	ids := []string{}
	for _, candidate := range l.photoMediaRows {
		if candidate.Hash == file.Hash && l.photoPathHiddenLocked(candidate.Path) == hidden {
			ids = append(ids, candidate.EntryID)
		}
	}
	l.photoMu.Unlock()
	if len(ids) < 2 {
		return ErrPhotoSearch
	}
	changed, err := l.catalog.PreferPhoto(id, file.Revision, ids)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(changed))
	for _, file := range changed {
		paths = append(paths, file.Path)
	}
	l.publishChange(Change{Kind: "photo-metadata", Paths: paths})
	return nil
}
