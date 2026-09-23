package sharedstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (s *Store) getOrWriteObject(ctx context.Context, stage *os.File, fingerprint []byte, plainLength, storedLength int64, encoding, kind, parent, op string) (string, []byte, error) {
	for {
		var id, state, storedKind string
		var wrapped []byte
		var plainLen, storedLen int64
		var storedEncoding string
		err := s.db.QueryRowContext(ctx, "SELECT object_id,state,node_key,plain_len,stored_len,kind,encoding FROM objects WHERE fingerprint=?", fingerprint).Scan(&id, &state, &wrapped, &plainLen, &storedLen, &storedKind, &storedEncoding)
		if err == nil {
			if state == "deleting" {
				return "", nil, ErrState
			}
			if parent != "" {
				if err = s.addDependency(ctx, parent, id); err != nil {
					return "", nil, err
				}
			}
			return s.verifyExisting(id, state, wrapped, plainLen, storedLen, plainLength, storedLength, storedEncoding, encoding, storedKind, kind)
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
				var stored bytes.Buffer
				e = decryptFile(candidate, &stored, id, key)
				if e == nil {
					_, e = decodeChunk(encoding, stored.Bytes(), plainLength, io.Discard)
				}
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
		_, e = tx.ExecContext(ctx, "INSERT INTO objects(object_id,fingerprint,plain_len,stored_len,encoding,node_key,state,write_op,kind) VALUES(?,?,?,?,?,?,'ready',?,?)", id, fingerprint, plainLength, storedLength, encoding, wrappedKey, op, kind)
		if e == nil && parent != "" {
			_, e = tx.ExecContext(ctx, "INSERT INTO object_dependencies(parent_id,child_id) VALUES(?,?)", parent, id)
		}
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

func (s *Store) addDependency(ctx context.Context, parent, child string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO object_dependencies(parent_id,child_id)
		SELECT ?,? WHERE EXISTS(SELECT 1 FROM objects WHERE object_id=? AND kind='manifest' AND state IN ('building','ready'))
		AND EXISTS(SELECT 1 FROM objects WHERE object_id=? AND kind='chunk' AND state='ready')`, parent, child, parent, child); err != nil {
		return err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM object_dependencies WHERE parent_id=? AND child_id=?", parent, child).Scan(&exists); err != nil || exists != 1 {
		return ErrState
	}
	return nil
}

func (s *Store) verifyExisting(id, state string, wrapped []byte, plainLength, storedLength, wantPlain, wantStored int64, encoding, wantEncoding, storedKind, wantKind string) (string, []byte, error) {
	if !validObjectID(id) || state != "ready" || plainLength != wantPlain || storedLength != wantStored || encoding != wantEncoding || storedKind != wantKind {
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
	var stored bytes.Buffer
	err = decryptFile(f, &stored, id, key)
	_ = f.Close()
	if err == nil {
		_, err = decodeChunk(encoding, stored.Bytes(), plainLength, io.Discard)
	}
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
