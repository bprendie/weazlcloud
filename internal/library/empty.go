package library

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
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
	pass, _, err := l.vault.Secrets()
	if err != nil {
		_ = os.RemoveAll(root)
		return catalog.File{}, err
	}
	snap, err := l.restic.PutBatch(ctx, restic.Repo{Location: l.repo, Password: pass}, root)
	if err != nil {
		_ = os.RemoveAll(root)
		return catalog.File{}, err
	}
	l.batchCommits.Add(1)
	stage.Snap = snap
	stage.BatchRoot = root
	stage.Object = filepath.Join(root, filepath.FromSlash(stage.Path))
	if err := l.writeStage(stage); err != nil {
		return catalog.File{}, err
	}
	return l.commitStaged(ctx, stage)
}
