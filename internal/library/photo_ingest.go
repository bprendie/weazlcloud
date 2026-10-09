package library

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (l *Library) PhotoIngestDir() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-ingest")
}

type PhotoUploadRoot struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Hidden bool   `json:"hidden,omitempty"`
}

func (l *Library) PhotoUploadRoots(ctx context.Context) ([]PhotoUploadRoot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return nil, vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return nil, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	roots := []PhotoUploadRoot{{ID: "root:photos", Name: "Photos", Hidden: l.photoPathHiddenLocked("Photos")}}
	for _, file := range l.photoRows {
		if file.Folder && file.Path != "Photos" && len(roots) < 200 {
			roots = append(roots, PhotoUploadRoot{ID: "root:" + file.EntryID, Name: file.Path, Hidden: l.photoPathHiddenLocked(file.Path)})
		}
	}
	return roots, nil
}

func (l *Library) ResolvePhotoUploadRoot(ctx context.Context, id string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return "", vault.ErrLocked
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return "", err
	}
	if id == "root:photos" {
		return "Photos", nil
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	file, ok := l.photoByID[strings.TrimPrefix(id, "root:")]
	if !strings.HasPrefix(id, "root:") || !ok || !file.Folder || !strings.HasPrefix(file.Path, PhotosRoot) {
		return "", catalog.ErrNotFound
	}
	return file.Path, nil
}

func (l *Library) CommitPhotoIngest(ctx context.Context, commit catalog.PhotoIngestCommit) (catalog.File, error) {
	return l.CommitPhotoIngestGuarded(ctx, commit, nil)
}

// CommitPhotoIngestGuarded acquires the library mutation lock before the grant
// guard, matching backup publication: library -> users -> catalog. The guard
// invokes publish synchronously; publish only calls the catalog, never Library.
func (l *Library) CommitPhotoIngestGuarded(ctx context.Context, commit catalog.PhotoIngestCommit, guard func(func() error) error) (catalog.File, error) {
	if group, _ := ctx.Value(photoCommitGroupKey{}).(string); group != "" && guard != nil {
		_, session := l.vault.State()
		return l.enqueuePhotoCatalog(photoCatalogRequest{ctx: ctx, session: session, commit: commit, group: group, guard: guard})
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return catalog.File{}, err
	}
	if !l.vault.Unlocked() {
		return catalog.File{}, vault.ErrLocked
	}
	if err := l.ensure(ctx); err != nil {
		return catalog.File{}, err
	}
	var file catalog.File
	publish := func() error { var e error; file, e = l.catalog.CommitPhotoIngest(commit); return e }
	var err error
	if guard != nil {
		err = guard(publish)
	} else {
		err = publish()
	}
	if err != nil {
		return catalog.File{}, err
	}
	paths := make([]string, 0, len(commit.Files)*2)
	for _, part := range commit.Files {
		paths = append(paths, part.From, part.To)
	}
	l.publishChange(Change{Kind: "put", Paths: paths})
	return file, nil
}
