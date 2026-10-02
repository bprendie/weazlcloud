package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
)

var ErrBackupChecksum = errors.New("backup checksum mismatch")

// StoreReader consumes a verified mobile reader without plaintext staging.
// Shared PrepareWithID uses the shared backend's authenticated encrypted staging;
// restic streams through stdin. Empty restic files need its existing batch API.
func (t *BackupTransaction) StoreReader(operation string, file catalog.File, body io.Reader) (catalog.File, error) {
	return t.storeReader(operation, file, body, false)
}

// RetryStoreReader lets the backend clear an aborted owner revision before a
// new private prepare. The returned actual Operation is journaled before CAS.
func (t *BackupTransaction) RetryStoreReader(operation string, file catalog.File, body io.Reader) (catalog.File, error) {
	return t.storeReader(operation, file, body, true)
}
func (t *BackupTransaction) storeReader(operation string, file catalog.File, body io.Reader, retry bool) (catalog.File, error) {
	if body == nil {
		return catalog.File{}, errors.New("backup reader is required")
	}
	h := sha256.New()
	r := &backupCountingReader{ctx: t.ctx, reader: io.TeeReader(io.LimitReader(body, file.Size+1), h)}
	var ref catalog.Reference
	var err error
	if t.lib.sharedStore != nil && t.lib.sharedWrites {
		var prepared sharedstore.Prepared
		var e error
		if retry {
			prepared, e = t.lib.sharedStore.Prepare(t.ctx, t.lib.ownerID, t.lib.vault, file.EntryID, file.Revision, r, file.Size)
		} else {
			prepared, e = t.lib.sharedStore.PrepareWithID(t.ctx, operation, t.lib.ownerID, t.lib.vault, file.EntryID, file.Revision, r, file.Size)
		}
		err = e
		ref = catalog.Reference{Backend: catalog.SharedBackend, Version: uint16(prepared.Reference.Version), Object: prepared.Reference.ObjectID, Operation: prepared.Operation, OwnerEntryID: prepared.Reference.EntryID, OwnerRevision: prepared.Reference.Revision}
	} else if file.Size == 0 {
		// Restic rejects empty stdin. No source plaintext is written: this directory
		// contains only one verified zero-length file and is removed on every exit.
		if _, err = io.Copy(io.Discard, r); err == nil && r.bytes == 0 {
			var root string
			root, err = os.MkdirTemp(filepath.Dir(t.lib.repo), ".weazl-backup-empty-")
			if err == nil {
				defer os.RemoveAll(root)
				name := filepath.Join(root, operation)
				err = os.WriteFile(name, nil, 0600)
				if err == nil {
					batch, e := t.lib.backend.PutBatch(t.ctx, root)
					err = e
					ref = resticReference(batch.Snapshot, name, "")
				}
			}
		}
	} else {
		ref, err = t.lib.backend.Put(t.ctx, operation, r)
	}
	if err != nil {
		return catalog.File{}, err
	}
	if r.bytes != file.Size || hex.EncodeToString(h.Sum(nil)) != file.Hash {
		if ref.Backend == catalog.SharedBackend {
			_ = t.lib.sharedStore.Recover(t.ctx, ref.Operation, false)
		}
		return catalog.File{}, ErrBackupChecksum
	}
	if ref.Backend != catalog.SharedBackend {
		ref.Operation = operation
	}
	file.Reference = &ref
	file.Snap, file.Object = ref.Snapshot, ref.Object
	return file, nil
}

type backupCountingReader struct {
	ctx    context.Context
	reader io.Reader
	bytes  int64
}

func (r *backupCountingReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	return n, err
}

// FinishBackup settles only catalog-proven shared operations, then releases
// replaced references. Both steps are idempotent across lost responses.
func (t *BackupTransaction) FinishBackup(plan catalog.BackupMutation) error {
	for _, f := range plan.Files {
		if f.Reference != nil && f.Reference.Backend == catalog.SharedBackend {
			retained := false
			for _, current := range t.lib.catalog.All() {
				if current.EntryID == f.EntryID && current.Reference != nil && *current.Reference == *f.Reference {
					retained = true
					break
				}
			}
			// Publication proof remains valid after a user replacement removes the old
			// reference. Do not try to reactivate its retired/released storage wrapper.
			if !retained {
				continue
			}
			if err := t.lib.sharedStore.Recover(t.ctx, f.Reference.Operation, true); err != nil {
				return err
			}
		}
	}
	for _, old := range plan.Guards {
		for _, f := range plan.Files {
			if f.EntryID == old.EntryID && old.Reference != nil && f.Reference != nil && *old.Reference != *f.Reference {
				if err := t.lib.releaseReference(t.ctx, old.Reference); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CheckBackupStorage rejects orphan references aborted by shared owner recovery
// before any catalog mutation. Pending references are still private and valid;
// already committed references must pass the ordinary owner read authorization.
func (t *BackupTransaction) CheckBackupStorage(plan catalog.BackupMutation) error {
	if t.lib.sharedStore == nil {
		return nil
	}
	pending, err := t.lib.sharedStore.Pending(t.ctx)
	if err != nil {
		return err
	}
	ready := make(map[string]bool)
	for _, op := range pending {
		ready[op] = true
	}
	for _, f := range plan.Files {
		if f.Reference == nil || f.Reference.Backend != catalog.SharedBackend || ready[f.Reference.Operation] {
			continue
		}
		if err := t.lib.readReference(t.ctx, *f.Reference, io.Discard); err != nil {
			return err
		}
	}
	return nil
}

func (t *BackupTransaction) AbortBackup(plan catalog.BackupMutation) error {
	if t.lib.sharedStore == nil {
		return nil
	}
	for _, f := range plan.Files {
		if f.Reference == nil || f.Reference.Backend != catalog.SharedBackend {
			continue
		}
		// Descendants in a folder rename retain already live source references.
		unchanged := false
		for _, old := range plan.Guards {
			if old.Reference != nil && *old.Reference == *f.Reference {
				unchanged = true
				break
			}
		}
		if !unchanged {
			if err := t.lib.sharedStore.Recover(t.ctx, f.Reference.Operation, false); err != nil && !errors.Is(err, sharedstore.ErrState) {
				return err
			}
		}
	}
	return nil
}
