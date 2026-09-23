package sharedstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Prepare verifies source bytes and persists a private pending reference.
func (s *Store) Prepare(ctx context.Context, owner string, v *vault.Vault, entry string, revision uint64, input io.Reader, expected int64) (Prepared, error) {
	if owner == "" || entry == "" || revision == 0 || v == nil || !v.Unlocked() || expected < -1 {
		return Prepared{}, ErrDenied
	}
	if err := s.clearAbortedRevision(ctx, owner, entry, revision); err != nil {
		return Prepared{}, err
	}
	opRaw, err := cryptox.Random(16)
	if err != nil {
		return Prepared{}, err
	}
	return s.PrepareWithID(ctx, opaqueID(opRaw), owner, v, entry, revision, input, expected)
}

// PrepareWithID makes upload finalization recoverable using the caller's durable operation ID.
func (s *Store) PrepareWithID(ctx context.Context, op, owner string, v *vault.Vault, entry string, revision uint64, input io.Reader, expected int64) (Prepared, error) {
	if owner == "" || entry == "" || revision == 0 || v == nil || !v.Unlocked() || expected < -1 {
		return Prepared{}, ErrDenied
	}
	if !validOperationID(op) {
		return Prepared{}, ErrDenied
	}
	var existingOwner, existingHash []byte
	var existingEntry, objectID, state, objectKind string
	var existingRevision uint64
	err := s.db.QueryRowContext(ctx, "SELECT owner_id,entry_id,revision,object_id,state,content_hash FROM operations WHERE op_id=?", op).Scan(&existingOwner, &existingEntry, &existingRevision, &objectID, &state, &existingHash)
	if err == nil {
		if !equalBytes(existingOwner, s.keys.ownerToken(owner)) || existingEntry != entry || existingRevision != revision || state == "aborted" || state == "released" {
			return Prepared{}, ErrState
		}
		h := sha256.New()
		n, hashErr := io.Copy(h, contextReader{ctx, input})
		if hashErr != nil {
			return Prepared{}, hashErr
		}
		if expected >= 0 && n != expected {
			return Prepared{}, ErrState
		}
		var storedFingerprint []byte
		var objectState string
		if hashErr = s.db.QueryRowContext(ctx, "SELECT fingerprint,state,kind FROM objects WHERE object_id=?", objectID).Scan(&storedFingerprint, &objectState, &objectKind); hashErr != nil {
			return Prepared{}, hashErr
		}
		matched := false
		if objectKind == "manifest" {
			matched = len(existingHash) == sha256.Size && equalBytes(existingHash, h.Sum(nil))
		} else {
			matched = equalBytes(s.keys.fingerprintFor(n, h.Sum(nil)), storedFingerprint)
		}
		if objectState != "ready" || !matched {
			return Prepared{}, ErrState
		}
		version := formatVersion
		if objectKind == "manifest" {
			version = chunkFormatVersion
		}
		return Prepared{Reference: Reference{Version: version, ObjectID: objectID, EntryID: entry, Revision: revision, Operation: op}, Operation: op}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Prepared{}, err
	}
	if v == nil || !v.Unlocked() {
		return Prepared{}, ErrDenied
	}
	stage, err := os.CreateTemp(filepath.Join(s.root, "shared-staging"), "source-")
	if err != nil {
		return Prepared{}, err
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	if err = stage.Chmod(0o600); err != nil {
		stage.Close()
		return Prepared{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(stage, h), contextReader{ctx, input})
	if err != nil {
		stage.Close()
		return Prepared{}, err
	}
	if expected >= 0 && n != expected {
		stage.Close()
		return Prepared{}, fmt.Errorf("source length mismatch: got %d", n)
	}
	if err = stage.Sync(); err != nil {
		stage.Close()
		return Prepared{}, err
	}
	if _, err = stage.Seek(0, io.SeekStart); err != nil {
		stage.Close()
		return Prepared{}, err
	}
	contentHash := h.Sum(nil)
	objectID, key, err := s.writeChunkedManifest(ctx, op, stage, n)
	if err != nil {
		stage.Close()
		return Prepared{}, err
	}
	stage.Close()
	if err = fail(s.options, "object_ready"); err != nil {
		return Prepared{}, err
	}
	ownerKey := map[string]any{"version": chunkFormatVersion, "owner": owner, "entry": entry, "revision": revision, "object": objectID, "key": cryptox.B64(key)}
	plain, err := json.Marshal(ownerKey)
	cryptox.Zero(key)
	if err != nil {
		return Prepared{}, err
	}
	wrapper, err := v.Wrap(plain)
	cryptox.Zero(plain)
	if err != nil {
		return Prepared{}, err
	}
	ownerToken := s.keys.ownerToken(owner)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Prepared{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "INSERT INTO operations(op_id,owner_id,entry_id,revision,object_id,state,content_hash) VALUES(?,?,?,?,?,'prepared',?)", op, ownerToken, entry, revision, objectID, contentHash)
	if err != nil {
		return Prepared{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO owners(owner_id,entry_id,revision,object_id,wrapped_key,op_id,state) VALUES(?,?,?,?,?,?,'prepared')", ownerToken, entry, revision, objectID, wrapper, op)
	if err != nil {
		return Prepared{}, err
	}
	if err = tx.Commit(); err != nil {
		return Prepared{}, err
	}
	if err = fail(s.options, "prepared"); err != nil {
		return Prepared{}, err
	}
	return Prepared{Reference: Reference{Version: chunkFormatVersion, ObjectID: objectID, EntryID: entry, Revision: revision, Operation: op}, Operation: op}, nil
}

func validOperationID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
