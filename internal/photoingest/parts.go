package photoingest

import (
	"context"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
	"io"
	"strings"
)

func destinationName(spec Spec, part Component) string {
	if part.ID == "original" && spec.OriginalMode == "opaque-original-v1" {
		return part.Filename + ".opaque"
	}
	return part.Filename
}

// CreateParts allocates only the encrypted logical receipt. Parent transport owns
// all encrypted component staging/reservations and uses View.Upload.ID as identity.
func (m *Manager) CreateParts(ctx context.Context, res *filesvc.Resource, user users.User, spec Spec) (View, error) {
	if spec.Transport != "" && spec.Transport != "parts-v1" {
		return View{}, ErrInvalid
	}
	spec.Transport = "parts-v1"
	return m.Create(ctx, res, user, spec)
}

// FinalizeParts opens complete component streams. The parent holds its staging
// lease, rechecks current authorization and reserves commit workspace. Bytes are
// verified again before catalog publication, with no plaintext filesystem spool.
func (m *Manager) FinalizeParts(ctx context.Context, res *filesvc.Resource, user users.User, id, device string, open func(string) (io.ReadCloser, error)) (View, error) {
	return m.FinalizePartsGuarded(ctx, res, user, id, device, open, nil)
}

// FinalizePartsGuarded holds the caller's current device grant around atomic
// catalog publication. The callback must invoke publish synchronously exactly
// once on success; it must not invoke it if authorization has been revoked.
func (m *Manager) FinalizePartsGuarded(ctx context.Context, res *filesvc.Resource, user users.User, id, device string, open func(string) (io.ReadCloser, error), guard func(func() error) error) (View, error) {
	release := m.lock(user.ID, id)
	defer release()
	receipt, err := load(res, id)
	if err != nil {
		return View{}, err
	}
	if err := authorized(receipt, device); err != nil {
		return View{}, err
	}
	if receipt.Spec.Transport != "parts-v1" {
		return View{}, ErrInvalid
	}
	if receipt.Status == "stored" {
		return m.view(res, user, receipt)
	}
	if open == nil {
		return View{}, ErrInvalid
	}
	root, err := res.Lib.ResolvePhotoUploadRoot(ctx, receipt.Spec.RootID)
	if err != nil {
		return View{}, err
	}
	capture, err := receipt.Spec.Capture()
	if err != nil {
		return View{}, err
	}
	commit := catalog.PhotoIngestCommit{SourceNamespace: receipt.Spec.SourceNamespace, SourceAssetID: receipt.Spec.SourceAssetID, SourceMappingRevision: receipt.Spec.SourceMappingRevision, DeviceID: receipt.Spec.DeviceID, DeviceAssetID: receipt.Spec.DeviceAssetID, SourceRevision: receipt.Spec.SourceRevision, Hidden: receipt.Spec.Hidden, OpaqueOriginal: receipt.Spec.OriginalMode == "opaque-original-v1", AlbumIDs: receipt.Spec.AlbumIDs, Capture: capture}
	for _, part := range receipt.Spec.Components {
		to := strings.TrimSuffix(root, "/") + "/Mobile/" + receipt.Spec.DeviceID + "/" + receipt.Spec.DeviceAssetID + "/" + receipt.Spec.SourceRevision + "/" + destinationName(receipt.Spec, part)
		from := pendingPath(receipt, part)
		existing, e := res.Lib.Metadata(ctx, to)
		if e == nil {
			if existing.DeviceID != receipt.Spec.DeviceID || existing.DeviceAssetID != receipt.Spec.DeviceAssetID || existing.SourceRevision != receipt.Spec.SourceRevision || existing.Hash != part.SHA256 || existing.Size != part.Size {
				return View{}, upload.ErrIdempotencyConflict
			}
		} else {
			if !errors.Is(e, library.ErrFileNotFound) {
				return View{}, e
			}
			source, e := open(part.ID)
			if e != nil {
				return View{}, e
			}
			if source == nil {
				return View{}, ErrInvalid
			}
			reader, e := verifyMedia(source, part.MediaType)
			if e == nil {
				_, e = res.Lib.StorePhotoComponent(ctx, from, reader, part.Size, part.SHA256)
			}
			if errors.Is(e, library.ErrPhotoComponentChecksum) {
				e = upload.ErrHashMismatch
			}
			closeErr := source.Close()
			if e != nil {
				return View{}, e
			}
			if closeErr != nil {
				return View{}, closeErr
			}
		}
		commit.Files = append(commit.Files, catalog.PhotoIngestFile{ID: part.ID, From: from, To: to, Hash: part.SHA256, Size: part.Size, MediaType: part.MediaType})
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
