package photoingest

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

func authorized(receipt Receipt, device string) error {
	if receipt.Status == "cancelled" || device != "" && receipt.Spec.DeviceID != device {
		return upload.ErrNotFound
	}
	return nil
}

func (m *Manager) Status(res *filesvc.Resource, user users.User, id, device string) (View, error) {
	release := m.lock(user.ID, id)
	defer release()
	receipt, err := load(res, id)
	if err != nil {
		return View{}, err
	}
	if err := authorized(receipt, device); err != nil {
		return View{}, err
	}
	return m.view(res, user, receipt)
}

func (m *Manager) Append(ctx context.Context, res *filesvc.Resource, user users.User, id, device, component string, offset, size int64, hash string, source io.Reader) (View, error) {
	release := m.lock(user.ID, id)
	defer release()
	receipt, err := load(res, id)
	if err != nil {
		return View{}, err
	}
	if err := authorized(receipt, device); err != nil {
		return View{}, err
	}
	for i, part := range receipt.Spec.Components {
		if part.ID != component {
			continue
		}
		if receipt.Uploads[i] == "" {
			return View{}, upload.ErrNotFound
		}
		if _, err := m.uploads.Append(ctx, user, receipt.Uploads[i], offset, size, hash, source); err != nil {
			return View{}, err
		}
		return m.view(res, user, receipt)
	}
	return View{}, upload.ErrNotFound
}

func (m *Manager) Finalize(ctx context.Context, res *filesvc.Resource, user users.User, id, device string, capture *catalog.CaptureMetadata) (View, error) {
	return m.FinalizeGuarded(ctx, res, user, id, device, capture, nil)
}

// FinalizeGuarded retains ordered transport and guards only catalog publication,
// through Library's library -> users -> catalog lock order.
func (m *Manager) FinalizeGuarded(ctx context.Context, res *filesvc.Resource, user users.User, id, device string, capture *catalog.CaptureMetadata, guard func(func() error) error) (View, error) {
	release := m.lock(user.ID, id)
	defer release()
	receipt, err := load(res, id)
	if err != nil {
		return View{}, err
	}
	if err := authorized(receipt, device); err != nil {
		return View{}, err
	}
	if receipt.Status == "stored" {
		return m.view(res, user, receipt)
	}
	if receipt.Spec.Transport == "parts-v1" {
		return View{}, ErrInvalid
	}
	if capture != nil {
		// Persist the first finalize metadata before publishing originals. A lost
		// response cannot replace it with different metadata on a later retry.
		if receipt.Spec.CapturedAt == "" && receipt.FinalizeCapture == nil {
			receipt.FinalizeCapture = capture
			if err := save(res, receipt); err != nil {
				return View{}, err
			}
		}
	}
	// No component is committed until every expected component is uploaded.
	for i, part := range receipt.Spec.Components {
		if receipt.Uploads[i] == "" {
			return View{}, upload.ErrIncomplete
		}
		status, err := m.uploads.Status(user, receipt.Uploads[i])
		if err != nil {
			return View{}, err
		}
		if status.Offset != part.Size {
			return View{}, upload.ErrIncomplete
		}
	}
	root, err := res.Lib.ResolvePhotoUploadRoot(ctx, receipt.Spec.RootID)
	if err != nil {
		return View{}, err
	}
	commit := catalog.PhotoIngestCommit{SourceNamespace: receipt.Spec.SourceNamespace, SourceAssetID: receipt.Spec.SourceAssetID, SourceMappingRevision: receipt.Spec.SourceMappingRevision, DeviceID: receipt.Spec.DeviceID, DeviceAssetID: receipt.Spec.DeviceAssetID, SourceRevision: receipt.Spec.SourceRevision, Hidden: receipt.Spec.Hidden, OpaqueOriginal: receipt.Spec.OriginalMode == "opaque-original-v1", AlbumIDs: receipt.Spec.AlbumIDs}
	commit.Capture, err = receipt.Spec.Capture()
	if err != nil {
		return View{}, err
	}
	if commit.Capture == nil {
		commit.Capture = receipt.FinalizeCapture
	}
	for i, part := range receipt.Spec.Components {
		_, err := m.uploads.Finalize(ctx, user, receipt.Uploads[i], func(ctx context.Context, session upload.SessionView, source io.Reader) error {
			if existing, e := res.Lib.Metadata(ctx, session.Path); e == nil {
				if existing.Hash == part.SHA256 && existing.Size == part.Size {
					return nil
				}
				return upload.ErrIdempotencyConflict
			} else if !errors.Is(e, library.ErrFileNotFound) {
				return e
			}
			reader, err := verifyMedia(source, part.MediaType)
			if err != nil {
				return err
			}
			_, err = res.Lib.StorePhotoComponent(ctx, session.Path, reader, part.Size, part.SHA256)
			if errors.Is(err, library.ErrPhotoComponentChecksum) {
				return upload.ErrHashMismatch
			}
			return err
		})
		if err != nil {
			return View{}, err
		}
		commit.Files = append(commit.Files, catalog.PhotoIngestFile{ID: part.ID, From: pendingPath(receipt, part), To: strings.TrimSuffix(root, "/") + "/Mobile/" + receipt.Spec.DeviceID + "/" + receipt.Spec.DeviceAssetID + "/" + receipt.Spec.SourceRevision + "/" + destinationName(receipt.Spec, part), Hash: part.SHA256, Size: part.Size, MediaType: part.MediaType})
	}
	file, err := res.Lib.CommitPhotoIngestGuarded(ctx, commit, guard)
	if err != nil {
		return View{}, err
	}
	receipt.AssetID, receipt.Revision, receipt.Path, receipt.Status = file.EntryID, file.Revision, file.Path, "stored"
	if err := save(res, receipt); err != nil {
		return View{}, err
	}
	return m.view(res, user, receipt)
}

func (m *Manager) Cancel(res *filesvc.Resource, user users.User, id, device string) error {
	release := m.lock(user.ID, id)
	defer release()
	receipt, err := load(res, id)
	if err != nil {
		return err
	}
	if receipt.Spec.Transport == "parts-v1" {
		if device != "" && receipt.Spec.DeviceID != device {
			return upload.ErrNotFound
		}
		return cancelParts(res, receipt)
	}
	if err := authorized(receipt, device); err != nil {
		return err
	}
	if receipt.Status == "stored" {
		return upload.ErrIdempotencyConflict
	}
	for _, uploadID := range receipt.Uploads {
		if uploadID == "" {
			continue
		}
		status, err := m.uploads.Status(user, uploadID)
		if err != nil {
			return err
		}
		if status.Status == "complete" {
			return upload.ErrIdempotencyConflict
		}
	}
	for _, uploadID := range receipt.Uploads {
		if uploadID != "" {
			if err := m.uploads.Cancel(user, uploadID); err != nil {
				return err
			}
		}
	}
	receipt.Status = "cancelled"
	return save(res, receipt)
}
