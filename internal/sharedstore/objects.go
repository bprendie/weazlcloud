package sharedstore

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (s *Store) getOrWriteObject(ctx context.Context, stage *os.File, fingerprint []byte, length int64) (string, []byte, error) {
	for {
		var id, state string
		var wrapped []byte
		var storedLen int64
		err := s.db.QueryRowContext(ctx, "SELECT object_id,state,node_key,plain_len FROM objects WHERE fingerprint=?", fingerprint).Scan(&id, &state, &wrapped, &storedLen)
		if err == nil {
			if state == "deleting" {
				return "", nil, ErrState
			}
			return s.verifyExisting(id, state, wrapped, storedLen, length)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", nil, err
		}
		var e error
		raw, e := cryptox.Random(24)
		if e != nil {
			return "", nil, e
		}
		id = opaqueID(raw)
		key, e := cryptox.Random(32)
		if e != nil {
			return "", nil, e
		}
		tmp, e := os.CreateTemp(filepath.Join(s.root, "shared-staging"), "cipher-")
		if e != nil {
			cryptox.Zero(key)
			return "", nil, e
		}
		tmpPath := tmp.Name()
		_ = tmp.Chmod(0o600)
		if _, e = stage.Seek(0, io.SeekStart); e == nil {
			e = encryptFile(stage, tmp, id, key)
		}
		if e == nil {
			e = tmp.Sync()
		}
		if closeErr := tmp.Close(); e == nil {
			e = closeErr
		}
		if e == nil {
			var candidate *os.File
			candidate, e = os.Open(tmpPath)
			if e == nil {
				e = decryptFile(candidate, io.Discard, id, key)
				_ = candidate.Close()
			}
		}
		if e != nil {
			_ = os.Remove(tmpPath)
			cryptox.Zero(key)
			return "", nil, e
		}
		if e = fail(s.options, "cipher_synced"); e != nil {
			_ = os.Remove(tmpPath)
			cryptox.Zero(key)
			return "", nil, e
		}
		dest := s.objectPath(id)
		if e = os.Rename(tmpPath, dest); e != nil {
			_ = os.Remove(tmpPath)
			cryptox.Zero(key)
			return "", nil, e
		}
		if e = syncDir(filepath.Dir(dest)); e != nil {
			cryptox.Zero(key)
			return "", nil, e
		}
		if e = fail(s.options, "object_renamed"); e != nil {
			cryptox.Zero(key)
			return "", nil, e
		}
		wrappedKey, e := s.wrapNodeKey(key)
		if e != nil {
			cryptox.Zero(key)
			return "", nil, e
		}
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			cryptox.Zero(key)
			return "", nil, e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO objects(object_id,fingerprint,plain_len,node_key,state,write_op) VALUES(?,?,?,?,'ready','')", id, fingerprint, length, wrappedKey)
		if e == nil {
			e = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if e != nil {
			cryptox.Zero(key)
			var peer string
			if qerr := s.db.QueryRowContext(ctx, "SELECT object_id FROM objects WHERE fingerprint=?", fingerprint).Scan(&peer); qerr == nil {
				_ = os.Remove(dest)
				continue
			}
			return "", nil, e
		}
		if e = fail(s.options, "index_ready"); e != nil {
			cryptox.Zero(key)
			return "", nil, e
		}
		return id, key, nil
	}
}

func (s *Store) verifyExisting(id, state string, wrapped []byte, storedLen, length int64) (string, []byte, error) {
	if !validObjectID(id) || state != "ready" || storedLen != length {
		return "", nil, ErrState
	}
	key, err := s.unwrapNodeKey(wrapped)
	if err != nil {
		return "", nil, err
	}
	f, err := os.Open(s.objectPath(id))
	if err != nil {
		cryptox.Zero(key)
		return "", nil, ErrState
	}
	err = decryptFile(f, io.Discard, id, key)
	_ = f.Close()
	if err != nil {
		cryptox.Zero(key)
		return "", nil, ErrState
	}
	return id, key, nil
}

func validObjectID(id string) bool {
	if len(id) != 48 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *Store) objectPath(id string) string {
	return filepath.Join(s.root, "shared-objects", id+".wobj")
}
func (s *Store) wrapNodeKey(key []byte) ([]byte, error) {
	nonce, ciphertext, err := cryptox.Seal(s.keys.wrapping, key)
	return append(nonce, ciphertext...), err
}
func (s *Store) unwrapNodeKey(raw []byte) ([]byte, error) {
	if len(raw) < 12 {
		return nil, ErrState
	}
	return cryptox.Open(s.keys.wrapping, raw[:12], raw[12:])
}
