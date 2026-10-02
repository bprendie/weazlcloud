package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"io"
	"path"
	"strings"
)

// Old pre-coordinator sessions retain their destination. Bearer finalization
// stages privately and publishes through the same guarded catalog transaction.
func (l *Library) CommitLegacyPhotoStreamGuarded(ctx context.Context, sessionID, destination string, size int64, hash string, source io.Reader, capture *catalog.CaptureMetadata, guard func(func() error) error) error {
	parts := strings.Split(strings.TrimPrefix(destination, "Photos/Mobile/"), "/")
	if !strings.HasPrefix(destination, "Photos/Mobile/") || len(parts) < 3 {
		return catalog.ErrConflict
	}
	exists, err := l.legacyPhotoReceiptGuarded(ctx, destination, size, hash, guard)
	if err != nil || exists {
		return err
	}
	from := ".weazl-mobile-pending/legacy-" + sessionID + "/" + path.Base(destination)
	if _, err := l.StorePhotoComponent(ctx, from, source, size, hash); err != nil {
		return err
	}
	_, err = l.CommitPhotoIngestGuarded(ctx, catalog.PhotoIngestCommit{DeviceID: parts[0], DeviceAssetID: parts[1], SourceRevision: "legacy", Capture: capture, Files: []catalog.PhotoIngestFile{{ID: "original", From: from, To: destination, Size: size, Hash: hash, MediaType: photoMediaType(destination)}}}, guard)
	return err
}
func (l *Library) legacyPhotoReceiptGuarded(ctx context.Context, destination string, size int64, hash string, guard func(func() error) error) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return false, err
	}
	f, exists := l.catalog.Get(destination)
	if !exists {
		return false, nil
	}
	if f.Size != size || f.Hash != hash {
		return false, catalog.ErrRevisionMismatch
	}
	if guard != nil {
		if err := guard(func() error { return nil }); err != nil {
			return true, err
		}
	}
	return true, nil
}
