package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// MigrationFiles returns entries without persisting upgrades in read-only mode.
func (l *Library) MigrationFiles(ctx context.Context, readOnly bool) ([]catalog.File, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if readOnly {
		if !l.vault.Unlocked() {
			return nil, vault.ErrLocked
		}
		if err := l.catalog.LoadReadOnly(); err != nil {
			return nil, err
		}
		return l.catalog.All(), nil
	}
	if err := l.ensure(ctx); err != nil {
		return nil, err
	}
	return l.catalog.All(), nil
}

// MigrateToShared verifies one immutable Restic version before switching it.
func (l *Library) MigrateToShared(ctx context.Context, target catalog.File, reserve func(int64) (func(), error), progress func(string, int64) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if progress == nil {
		progress = func(string, int64) error { return nil }
	}
	if l.sharedStore == nil || l.ownerID == "" || !l.vault.Unlocked() {
		return errors.New("shared migration is not configured")
	}
	if err := l.ensure(ctx); err != nil {
		return err
	}
	var source catalog.File
	found := false
	for _, candidate := range l.catalog.All() {
		if candidate.EntryID == target.EntryID && candidate.Revision == target.Revision {
			source, found = candidate, true
			break
		}
	}
	if !found || source.Folder {
		return catalog.ErrRevisionMismatch
	}
	if source.Reference != nil && source.Reference.Backend == catalog.SharedBackend {
		return nil
	}
	if source.Size < 0 || source.Hash == "" {
		return errors.New("source metadata is incomplete")
	}
	expectedHash, err := hex.DecodeString(strings.TrimSpace(source.Hash))
	if err != nil || len(expectedHash) != sha256.Size {
		return errors.New("source hash is invalid")
	}
	sourceRef, err := l.backend.Capture(source)
	if err != nil {
		return err
	}
	releaseSpace, err := reserve(source.Size)
	if err != nil {
		return err
	}
	defer releaseSpace()
	releaseSource, err := l.backend.Hold(sourceRef)
	if err != nil {
		return err
	}
	defer releaseSource()
	nextRevision := source.Revision + 1
	if nextRevision == 0 {
		return catalog.ErrRevisionOverflow
	}
	if err = progress("source-pinned", 0); err != nil {
		return err
	}
	pr, pw := io.Pipe()
	sourceHash := sha256.New()
	type sourceResult struct {
		size int64
		err  error
	}
	finished := make(chan sourceResult, 1)
	go func() {
		count := &migrationCountWriter{writer: io.MultiWriter(pw, sourceHash)}
		readErr := l.readReference(ctx, sourceRef, count)
		if readErr != nil {
			_ = pw.CloseWithError(readErr)
		} else {
			_ = pw.Close()
		}
		finished <- sourceResult{size: count.size, err: readErr}
	}()
	prepared, prepareErr := l.sharedStore.Prepare(ctx, l.ownerID, l.vault, source.EntryID, nextRevision, pr, source.Size)
	_ = pr.Close()
	result := <-finished
	if prepareErr != nil {
		return prepareErr
	}
	abort := func() { _ = l.sharedStore.Recover(context.Background(), prepared.Operation, false) }
	if result.err != nil {
		abort()
		return result.err
	}
	if result.size != source.Size || !equalHash(sourceHash.Sum(nil), expectedHash) {
		abort()
		return errors.New("source content does not match catalog metadata")
	}
	if err = progress("copied", result.size); err != nil {
		abort()
		return err
	}
	if err = l.sharedStore.VerifyPrepared(ctx, l.ownerID, l.vault, prepared, source.Size, expectedHash); err != nil {
		abort()
		return err
	}
	if err = progress("destination-verified", result.size); err != nil {
		abort()
		return err
	}
	ref := catalog.Reference{Backend: catalog.SharedBackend, Version: uint16(prepared.Reference.Version), Object: prepared.Reference.ObjectID, Operation: prepared.Operation, OwnerEntryID: source.EntryID, OwnerRevision: nextRevision}
	if _, err = l.catalog.SwitchReference(source.EntryID, source.Revision, sourceRef, ref); err != nil {
		abort()
		return err
	}
	if err = progress("catalog-switched", result.size); err != nil {
		return err
	}
	if err = l.sharedStore.MarkPublished(ctx, prepared.Operation); err != nil {
		return err
	}
	if err = l.sharedStore.Commit(ctx, prepared.Operation); err != nil {
		return err
	}
	return progress("complete", result.size)
}

type migrationCountWriter struct {
	writer io.Writer
	size   int64
}

func (w *migrationCountWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.size += int64(n)
	return n, err
}

func equalHash(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// VerifyMigratedFile reads the full shared file and compares its catalog hash.
func (l *Library) VerifyMigratedFile(ctx context.Context, target catalog.File) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return vault.ErrLocked
	}
	if err := l.catalog.LoadReadOnly(); err != nil {
		return err
	}
	for _, current := range l.catalog.All() {
		if current.EntryID != target.EntryID || current.Revision != target.Revision {
			continue
		}
		if current.Reference == nil || current.Reference.Backend != catalog.SharedBackend || current.Size < 0 {
			return catalog.ErrUnknownReference
		}
		expected, err := hex.DecodeString(current.Hash)
		if err != nil || len(expected) != sha256.Size {
			return errors.New("source metadata is invalid")
		}
		h := sha256.New()
		count := &migrationCountWriter{writer: h}
		if err = l.readReference(ctx, *current.Reference, count); err != nil {
			return err
		}
		if count.size != current.Size || !equalHash(h.Sum(nil), expected) {
			return errors.New("shared content does not match catalog metadata")
		}
		return nil
	}
	return catalog.ErrRevisionMismatch
}
