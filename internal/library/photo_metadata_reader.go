package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
)

func (r *metadataResolver) openReader(ctx context.Context) {
	if os.Getenv("WEAZLCLOUD_METADATA_READER") == "off" {
		return
	}
	r.reader, r.releaseReader = r.lib.borrowPreviewReader(ctx)
}

func (r *metadataResolver) closeReader() {
	if r.releaseReader != nil {
		r.releaseReader()
		r.releaseReader = nil
	}
	r.reader = nil
}

func (r *metadataResolver) readPersistent(ctx context.Context, file catalog.File, limit int64) ([]byte, error) {
	l := r.lib
	l.mu.Lock()
	err := l.validatePreviewLocked(ctx, file)
	var ref catalog.Reference
	var release func()
	if err == nil {
		ref, err = l.capture(file)
	}
	if err == nil {
		release, err = l.holdReference(ref)
	}
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer release()
	select {
	case thumbnailReaders <- struct{}{}:
		defer func() { <-thumbnailReaders }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	result, err := r.reader.Read(ctx, ref.Snapshot, ref.Object, int(min(file.Size, limit)))
	if err != nil {
		return nil, err
	}
	valid := result.Size == uint64(file.Size) && int64(len(result.Body)) == min(file.Size, limit)
	if valid && file.Size <= limit {
		hash := sha256.Sum256(result.Body)
		valid = hex.EncodeToString(hash[:]) == file.Hash
	}
	if !valid {
		clear(result.Body)
		return nil, restic.ErrReader
	}
	if err = l.validatePreview(ctx, file); err != nil {
		clear(result.Body)
		return nil, err
	}
	return result.Body, nil
}
