package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/vault"
	"io"
	"os"
	"strings"
	"time"
)

var ErrPhotoComponentChecksum = errors.New("photo component size or checksum mismatch")

// StorePhotoComponent streams into encrypted backend storage without raw staging.
// The caller reserves workspace/quota and validates media type before calling.
// A component is private outside Photos until CommitPhotoIngest publishes the pair.
func (l *Library) StorePhotoComponent(ctx context.Context, name string, body io.Reader, size int64, hash string) (catalog.File, error) {
	if !strings.HasPrefix(name, ".weazl-mobile-pending/") || size < 1 {
		return catalog.File{}, catalog.ErrConflict
	}
	clean, err := cleanPath(name)
	if err != nil || clean != name {
		return catalog.File{}, catalog.ErrConflict
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return catalog.File{}, vault.ErrLocked
	}
	if err := l.ensure(ctx); err != nil {
		return catalog.File{}, err
	}
	if f, ok := l.catalog.Get(name); ok {
		if f.Size == size && f.Hash == hash {
			if f.Reference != nil && f.Reference.Backend == catalog.SharedBackend {
				if err := l.sharedStore.Recover(ctx, f.Reference.Operation, true); err != nil {
					return catalog.File{}, err
				}
			}
			return f, nil
		}
		return catalog.File{}, catalog.ErrRevisionMismatch
	}
	intentPath, err := l.photoComponentIntentPath(name, size, hash)
	if err != nil {
		return catalog.File{}, err
	}
	h := sha256.New()
	r := &photoStreamReader{ctx: ctx, reader: io.TeeReader(io.LimitReader(body, size+1), h)}
	entryID, revision, err := l.catalog.NextIdentity(name)
	if err != nil {
		return catalog.File{}, err
	}
	var ref catalog.Reference
	if l.sharedStore != nil && l.sharedWrites {
		intent, e := l.loadPhotoComponentIntent(intentPath, name, size, hash)
		if e != nil {
			return catalog.File{}, e
		}
		entryID, revision = intent.File.EntryID, intent.File.Revision
		prepared, e := l.sharedStore.PrepareWithID(ctx, intent.Operation, l.ownerID, l.vault, entryID, revision, r, size)
		if errors.Is(e, sharedstore.ErrState) && r.size == 0 {
			prepared, e = l.sharedStore.Prepare(ctx, l.ownerID, l.vault, entryID, revision, r, size)
		}
		if e == nil {
			intent.Operation = prepared.Operation
			intent.File.Reference = &catalog.Reference{Backend: catalog.SharedBackend, Version: uint16(prepared.Reference.Version), Object: prepared.Reference.ObjectID, Operation: prepared.Operation, OwnerEntryID: prepared.Reference.EntryID, OwnerRevision: prepared.Reference.Revision}
			if e = l.savePhotoComponentIntent(intentPath, intent); e != nil {
				return catalog.File{}, e
			}
		}
		err = e
		ref = catalog.Reference{Backend: catalog.SharedBackend, Version: uint16(prepared.Reference.Version), Object: prepared.Reference.ObjectID, Operation: prepared.Operation, OwnerEntryID: prepared.Reference.EntryID, OwnerRevision: prepared.Reference.Revision}
	} else {
		// Restic 0.18 requires stdin-filename's parents to exist. Use an opaque
		// flat storage key; the private logical path belongs only in the catalog.
		ref, err = l.backend.Put(ctx, entryID, r)
	}
	if err != nil {
		return catalog.File{}, err
	}
	if r.size != size || hex.EncodeToString(h.Sum(nil)) != hash {
		if ref.Backend == catalog.SharedBackend {
			_ = l.sharedStore.Recover(ctx, ref.Operation, false)
			_ = os.Remove(intentPath)
		}
		return catalog.File{}, ErrPhotoComponentChecksum
	}
	f := catalog.File{EntryID: entryID, Revision: revision, Path: name, Size: size, Hash: hash, Present: true, Mtime: time.Now().UTC(), ImportedAt: time.Now().UTC(), Reference: &ref, Snap: ref.Snapshot, Object: ref.Object}
	if err := l.catalog.Put(f); err != nil {
		return catalog.File{}, err
	}
	if ref.Backend == catalog.SharedBackend {
		if err := l.sharedStore.Recover(ctx, ref.Operation, true); err != nil {
			return catalog.File{}, err
		}
	}
	if ref.Backend == catalog.SharedBackend {
		if err := os.Remove(intentPath); err != nil && !os.IsNotExist(err) {
			return catalog.File{}, err
		}
	}
	stored, _ := l.catalog.Get(name)
	return stored, nil
}

type photoStreamReader struct {
	ctx    context.Context
	reader io.Reader
	size   int64
}

func (r *photoStreamReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	r.size += int64(n)
	return n, err
}
