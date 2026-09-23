package library

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func (l *Library) commitEmptyStage(ctx context.Context, stage stagedUpload) (catalog.File, error) {
	id, err := randomBatchID()
	if err != nil {
		return catalog.File{}, err
	}
	root := filepath.Join(filepath.Dir(l.repo), ".weazl-empty-"+id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return catalog.File{}, err
	}
	if err := linkStage(root, l.repo, stage); err != nil {
		_ = os.RemoveAll(root)
		return catalog.File{}, err
	}
	batch, err := l.backend.PutBatch(ctx, root)
	if err != nil {
		_ = os.RemoveAll(root)
		return catalog.File{}, err
	}
	l.batchCommits.Add(1)
	stage.Snap = batch.Snapshot
	stage.BatchRoot = root
	stage.Object = filepath.Join(root, filepath.FromSlash(stage.Path))
	ref := resticReference(stage.Snap, stage.Object, stage.Hash)
	stage.Reference = &ref
	if err := l.writeStage(stage); err != nil {
		return catalog.File{}, err
	}
	return l.commitStaged(ctx, stage)
}
