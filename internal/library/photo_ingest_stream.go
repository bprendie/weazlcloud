package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
)

var ErrPhotoComponentChecksum = errors.New("photo component size or checksum mismatch")

// StorePhotoComponent retains ownership of body until all backend reads stop.
// Only identity reservation and catalog publication require the library mutex.
func (l *Library) StorePhotoComponent(parent context.Context, name string, body io.Reader, size int64, checksum string) (catalog.File, error) {
	if !strings.HasPrefix(name, ".weazl-mobile-pending/") || size < 1 || size == math.MaxInt64 {
		return catalog.File{}, catalog.ErrConflict
	}
	clean, err := cleanPath(name)
	if err != nil || clean != name {
		return catalog.File{}, catalog.ErrConflict
	}
	ctx, done, err := l.beginPhotoComponent(parent)
	if err != nil {
		return catalog.File{}, err
	}
	defer done()
	unlock, err := l.gatePhotoComponents(ctx, name)
	if err != nil {
		return catalog.File{}, err
	}
	defer unlock()
	l.mu.Lock()
	if err = ctx.Err(); err == nil {
		err = l.ensure(ctx)
	}
	if err != nil {
		l.mu.Unlock()
		return catalog.File{}, err
	}
	intentPath, err := l.photoComponentIntentPath(name, size, checksum)
	if err == nil {
		var canceled bool
		canceled, err = l.photoComponentCanceled(intentPath, name, size, checksum)
		if canceled {
			err = context.Canceled
		}
	}
	if err != nil {
		l.mu.Unlock()
		return catalog.File{}, err
	}
	_, session := l.vault.State()
	store, owner, shared := l.sharedStore, l.ownerID, l.sharedStore != nil && l.sharedWrites
	if f, ok := l.catalog.Get(name); ok {
		l.mu.Unlock()
		if f.Size != size || f.Hash != checksum {
			return catalog.File{}, catalog.ErrRevisionMismatch
		}
		if f.Reference != nil && f.Reference.Backend == catalog.SharedBackend {
			if store == nil {
				return catalog.File{}, catalog.ErrUnknownReference
			}
			if err = store.Recover(ctx, f.Reference.Operation, true); err != nil {
				return catalog.File{}, err
			}
		}
		if err = removePhotoComponentIntent(intentPath); err != nil {
			return catalog.File{}, err
		}
		return f, nil
	}
	intent, err := l.loadPhotoComponentIntent(intentPath, name, size, checksum)
	l.mu.Unlock()
	if err != nil {
		return catalog.File{}, err
	}
	if intent.Canceled {
		return catalog.File{}, context.Canceled
	}
	f := intent.File
	r := &photoStreamReader{ctx: ctx, reader: io.LimitReader(body, size+1), hash: sha256.New(), expected: size, checksum: checksum}
	var ref catalog.Reference
	// Only verified Restic references are recorded for byte-free restart retries.
	// Shared intents may originate from older versions which saved before verify.
	if f.Reference != nil && f.Reference.Backend == catalog.ResticBackend {
		ref = *f.Reference
	} else if shared {
		prepared, e := store.PrepareWithID(ctx, intent.Operation, owner, l.vault, f.EntryID, f.Revision, r, size)
		if errors.Is(e, sharedstore.ErrState) && r.size == 0 {
			prepared, e = store.Prepare(ctx, owner, l.vault, f.EntryID, f.Revision, r, size)
		}
		err = e
		if err == nil {
			intent.Operation = prepared.Operation
			ref = catalog.Reference{Backend: catalog.SharedBackend, Version: uint16(prepared.Reference.Version), Object: prepared.Reference.ObjectID, Operation: prepared.Operation, OwnerEntryID: prepared.Reference.EntryID, OwnerRevision: prepared.Reference.Revision}
		}
	} else {
		var release func()
		ref, release, err = l.putPhotoStream(ctx, StreamInput{Name: f.EntryID, Size: size, Hash: checksum, Reader: r}, r)
		if release != nil {
			defer release()
		}
	}
	if err != nil {
		if r.invalid {
			return catalog.File{}, ErrPhotoComponentChecksum
		}
		if canceled := ctx.Err(); canceled != nil {
			return catalog.File{}, canceled
		}
		return catalog.File{}, err
	}
	if f.Reference == nil || f.Reference.Backend != catalog.ResticBackend {
		if r.size != size || hex.EncodeToString(r.hash.Sum(nil)) != checksum || r.readErr != nil {
			if ref.Backend == catalog.SharedBackend {
				_ = store.Recover(ctx, ref.Operation, false)
				_ = os.Remove(intentPath)
			}
			return catalog.File{}, ErrPhotoComponentChecksum
		}
		f.Reference, f.Snap, f.Object = &ref, ref.Snapshot, ref.Object
		f.Present, f.Mtime, f.ImportedAt = true, time.Now().UTC(), time.Now().UTC()
		intent.File = f
		if err = l.savePhotoComponentIntent(intentPath, intent); err != nil {
			return catalog.File{}, err
		}
	}
	f, err = l.enqueuePhotoCatalog(photoCatalogRequest{ctx: ctx, session: session, file: f})
	if err != nil {
		return catalog.File{}, err
	}
	// The name gate serializes recovery with cancellation cleanup, without l.mu.
	if ref.Backend == catalog.SharedBackend {
		if err = store.Recover(ctx, ref.Operation, true); err != nil {
			return catalog.File{}, err
		}
	}
	if err = removePhotoComponentIntent(intentPath); err != nil {
		return catalog.File{}, err
	}
	return f, nil
}

type photoStreamReader struct {
	ctx      context.Context
	reader   io.Reader
	size     int64
	hash     hash.Hash
	expected int64
	checksum string
	invalid  bool
	readErr  error
}

func (r *photoStreamReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	r.size += int64(n)
	if r.hash != nil {
		_, _ = r.hash.Write(p[:n])
	}
	if err != nil && err != io.EOF {
		r.readErr = err
	}
	if r.hash != nil && (r.size > r.expected || err == io.EOF && (r.size != r.expected || hex.EncodeToString(r.hash.Sum(nil)) != r.checksum)) {
		r.invalid = true
		return n, ErrPhotoComponentChecksum
	}
	return n, err
}
