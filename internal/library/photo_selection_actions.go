package library

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) PreparePhotoSelectionExport(ctx context.Context, id string, hidden bool) ([]PhotoExport, func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	files, err := l.resolvePhotoSelectionLocked(ctx, id, hidden)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(files))
	for i, file := range files {
		ids[i] = file.EntryID
	}
	return l.preparePhotoExportLocked(ctx, ids, hidden)
}

func (l *Library) PreparePhotoSelectionArchive(ctx context.Context, id string, hidden bool) (ArchiveManifest, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	files, err := l.resolvePhotoSelectionLocked(ctx, id, hidden)
	if err != nil {
		return ArchiveManifest{}, err
	}
	paths := make([]string, 0, len(files)*2)
	l.photoMu.Lock()
	for _, file := range files {
		paths = append(paths, file.Path)
		for _, component := range file.PhotoComponents {
			part, ok := l.photoByID[component.AssetID]
			if component.ID != "original" && ok && l.photoPathHiddenLocked(part.Path) == hidden {
				paths = append(paths, part.Path)
			}
		}
	}
	l.photoMu.Unlock()
	return l.prepareArchiveLocked(ctx, paths)
}

func (l *Library) ChangePhotoSelectionAlbum(ctx context.Context, selection, album string, revision uint64, hidden, remove bool) (catalog.Album, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	files, err := l.resolvePhotoSelectionLocked(ctx, selection, hidden)
	if err != nil {
		return catalog.Album{}, err
	}
	ids := make([]string, len(files))
	for i, file := range files {
		ids[i] = file.EntryID
	}
	var addIDs, removeIDs []string
	if remove {
		removeIDs = ids
	} else {
		addIDs = ids
	}
	updated, err := l.catalog.ChangeAlbumMembers(album, revision, addIDs, removeIDs)
	if err != nil {
		return catalog.Album{}, err
	}
	l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	return updated, nil
}

func (l *Library) SavePhotoSelectionAlbum(ctx context.Context, selection string, hidden bool, album catalog.Album) (catalog.Album, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if album.ID != "" || len(album.AssetIDs) != 0 {
		return catalog.Album{}, catalog.ErrAlbumInvalid
	}
	files, err := l.resolvePhotoSelectionLocked(ctx, selection, hidden)
	if err != nil {
		return catalog.Album{}, err
	}
	album.AssetIDs = make([]string, len(files))
	for i, file := range files {
		album.AssetIDs[i] = file.EntryID
	}
	updated, err := l.catalog.SaveAlbum(album)
	if err != nil {
		return catalog.Album{}, err
	}
	l.publishChange(Change{Kind: "photo-album", Paths: []string{"Photos"}})
	return updated, nil
}

func (l *Library) DeletePhotoSelection(ctx context.Context, selection string, hidden bool) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	files, err := l.resolvePhotoSelectionLocked(ctx, selection, hidden)
	if err != nil {
		return 0, err
	}
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	affected, err := l.catalog.DeletePaths(paths)
	if err != nil {
		return 0, err
	}
	l.publishChange(Change{Kind: "delete", Paths: affected})
	return len(files), nil
}
