package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) commitSharedStaged(ctx context.Context, stage stagedUpload) (catalog.File, error) {
	if l.sharedStore == nil || l.ownerID == "" {
		return catalog.File{}, errors.New("shared storage is not configured")
	}
	if !l.vault.Unlocked() {
		return catalog.File{}, errors.New("vault is locked")
	}
	if err := l.backend.Ensure(ctx); err != nil {
		return catalog.File{}, err
	}
	if err := l.catalog.Load(); err != nil {
		return catalog.File{}, err
	}
	if stage.EntryID == "" || stage.Revision == 0 {
		id, revision, err := l.catalog.NextIdentity(stage.Path)
		if err != nil {
			return catalog.File{}, err
		}
		stage.EntryID, stage.Revision = id, revision
	}
	if stage.OldReference == nil {
		if old, ok := l.catalog.Get(stage.Path); ok && old.Reference != nil {
			ref := *old.Reference
			stage.OldReference = &ref
		}
	}
	if err := l.writeStage(stage); err != nil {
		return catalog.File{}, err
	}
	file, err := os.Open(filepath.Join(l.stageDir(), stage.Data))
	if err != nil {
		return catalog.File{}, err
	}
	prepared, prepErr := l.sharedStore.PrepareWithID(ctx, stage.ID, l.ownerID, l.vault, stage.EntryID, stage.Revision, file, stage.Size)
	_ = file.Close()
	if prepErr != nil {
		return catalog.File{}, prepErr
	}
	ref := catalog.Reference{Backend: catalog.SharedBackend, Version: uint16(prepared.Reference.Version), Object: prepared.Reference.ObjectID, Operation: prepared.Operation, OwnerEntryID: prepared.Reference.EntryID, OwnerRevision: prepared.Reference.Revision}
	if stage.Reference != nil && *stage.Reference != ref {
		return catalog.File{}, catalog.ErrUnknownReference
	}
	stage.Reference = &ref
	if err = l.writeStage(stage); err != nil {
		return catalog.File{}, err
	}
	f := catalog.File{EntryID: stage.EntryID, Revision: stage.Revision, Path: stage.Path, Size: stage.Size, Mtime: stage.Mtime, Hash: stage.Hash, Object: ref.Object, Reference: &ref, Present: true}
	if err = l.catalog.Put(f); err != nil {
		return catalog.File{}, err
	}
	if err = l.sharedStore.MarkPublished(ctx, stage.ID); err != nil {
		return catalog.File{}, err
	}
	if err = l.sharedStore.Commit(ctx, stage.ID); err != nil {
		return catalog.File{}, err
	}
	if stage.OldReference != nil && *stage.OldReference != ref {
		if err = l.releaseReference(ctx, stage.OldReference); err != nil {
			return catalog.File{}, err
		}
	}
	l.publishChange(Change{Kind: "put", Paths: []string{f.Path}})
	if err = l.removeStage(stage); err != nil {
		return catalog.File{}, err
	}
	return f, nil
}
