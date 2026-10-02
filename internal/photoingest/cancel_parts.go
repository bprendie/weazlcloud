package photoingest

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/upload"
)

// Called under the coordinator receipt gate. It never calls the transport engine:
// CancelCoordinated may hold that engine's gate while invoking this cleanup.
func cancelParts(res *filesvc.Resource, receipt Receipt) error {
	// The durable stored receipt remains proof of publication even if the user
	// later purges the canonical asset from the catalog.
	if receipt.Status == "stored" {
		return upload.ErrIdempotencyConflict
	}
	commit := catalog.PhotoIngestCommit{DeviceID: receipt.Spec.DeviceID, DeviceAssetID: receipt.Spec.DeviceAssetID, SourceRevision: receipt.Spec.SourceRevision}
	for _, p := range receipt.Spec.Components {
		commit.Files = append(commit.Files, catalog.PhotoIngestFile{ID: p.ID, From: pendingPath(receipt, p), Hash: p.SHA256, Size: p.Size})
	}
	file, err := res.Lib.CancelPhotoIngest(context.Background(), commit, func() error {
		receipt.Status = "cancelled"
		return save(res, receipt)
	})
	if err != nil {
		return err
	}
	if file.EntryID != "" {
		receipt.AssetID, receipt.Revision, receipt.Path, receipt.Status = file.EntryID, file.Revision, file.Path, "stored"
		if err := save(res, receipt); err != nil {
			return err
		}
		return upload.ErrIdempotencyConflict
	}
	return nil
}
