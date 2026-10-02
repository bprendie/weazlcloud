package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (l *Library) PhotoCollections(ctx context.Context, after string, position *catalog.SyncPosition, limit int) (catalog.CollectionPage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return catalog.CollectionPage{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return catalog.CollectionPage{}, err
	}
	return l.catalog.CollectionPage(after, position, limit)
}
func (l *Library) SavePhotoCollection(ctx context.Context, f catalog.CollectionFolder) (catalog.CollectionFolder, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return f, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return f, err
	}
	f, err := l.catalog.SaveCollectionFolder(f)
	if err == nil {
		l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	}
	return f, err
}
func (l *Library) DeletePhotoCollection(ctx context.Context, id string, revision uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return err
	}
	err := l.catalog.DeleteCollectionFolder(id, revision)
	if err == nil {
		l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	}
	return err
}

// PhotoCollectionChanges negotiates schema 2 separately from legacy photo sync.
// Scan progress advances across all journal entries, including unrelated Files.
type PhotoCollectionDelta struct {
	Schema     int                    `json:"schema"`
	Changes    []catalog.ChangeRecord `json:"changes"`
	Position   catalog.SyncPosition   `json:"position"`
	HasMore    bool                   `json:"has_more"`
	NextCursor string                 `json:"next_cursor,omitempty"`
	Checkpoint string                 `json:"checkpoint,omitempty"`
}

func (l *Library) PhotoCollectionChanges(ctx context.Context, position catalog.SyncPosition, limit int) (PhotoCollectionDelta, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoCollectionDelta{}, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoCollectionDelta{}, err
	}
	records, next, more, err := l.catalog.ChangesAfter(position, limit)
	if err != nil {
		return PhotoCollectionDelta{}, err
	}
	page := PhotoCollectionDelta{Schema: 2, Changes: []catalog.ChangeRecord{}, Position: next, HasMore: more}
	for _, r := range records {
		if r.Kind != "album" && r.Kind != "collection" && r.Kind != "source-mapping" {
			continue
		}
		if r.SourceMapping != nil && r.SourceMapping.Kind == "asset" {
			continue
		}
		if r.Album != nil {
			r.Album.AssetIDs = nil
			r.Album.CoverID = ""
		}
		page.Changes = append(page.Changes, r)
	}
	return page, nil
}
