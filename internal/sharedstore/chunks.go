package sharedstore

import (
	"bytes"
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
	"github.com/klauspost/compress/zstd"
	"github.com/restic/chunker"
)

const (
	chunkMin      = 512 << 10
	chunkMax      = 8 << 20
	chunkAvgBits  = 20
	chunkPol      = chunker.Pol(0x3DA3358B4DC173)
	chunkSettings = "restic-rabin-v1;pol=3da3358b4dc173;min=524288;max=8388608;avg_bits=20;codec=zstd-fastest-if-saves-64-v1"
)

type manifestLine struct {
	Type     string `json:"type"`
	Version  int    `json:"version,omitempty"`
	Chunker  string `json:"chunker,omitempty"`
	Length   int64  `json:"length,omitempty"`
	Count    uint64 `json:"count,omitempty"`
	ObjectID string `json:"object_id,omitempty"`
	Key      string `json:"key,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
}

func ensureChunkSettings(db *sql.DB) error {
	if _, err := db.Exec(`INSERT OR IGNORE INTO format_settings(setting,value) VALUES('chunking',?)`, chunkSettings); err != nil {
		return err
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM format_settings WHERE setting='chunking'`).Scan(&value); err != nil {
		return err
	}
	if value != chunkSettings {
		return fmt.Errorf("unsupported shared chunking parameters: %w", ErrFormat)
	}
	return nil
}

func (s *Store) writeChunkedManifest(ctx context.Context, op string, stage io.Reader, length int64, contentHash []byte) (string, []byte, error) {
	raw, err := cryptox.Random(24)
	if err != nil {
		return "", nil, err
	}
	id := opaqueID(raw)
	key, err := cryptox.Random(32)
	if err != nil {
		return "", nil, err
	}
	wrapped, err := s.wrapNodeKey(key)
	if err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	fpRaw, err := cryptox.Random(32)
	if err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO objects(object_id,fingerprint,plain_len,node_key,state,write_op,kind) VALUES(?,?,?,?,'building',?,'manifest')`, id, fpRaw, length, wrapped, op); err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	ready := false
	defer func() {
		if !ready {
			_, _ = s.db.Exec(`UPDATE objects SET state='deleting' WHERE object_id=? AND state='building'`, id)
			_ = s.removeClaimedObject(id)
		}
	}()
	plain, err := s.newEncryptedStage("manifest-")
	if err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	defer plain.Close()
	writeLine := func(line manifestLine) error {
		encoded, marshalErr := json.Marshal(line)
		if marshalErr != nil {
			return marshalErr
		}
		encoded = append(encoded, '\n')
		return writeAll(plain, encoded)
	}
	if err = writeLine(manifestLine{Type: "header", Version: chunkFormatVersion, Chunker: chunkSettings}); err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	hash := sha256.New()
	c := chunker.New(io.TeeReader(contextReader{ctx, stage}, hash), chunkPol, chunker.WithBoundaries(chunkMin, chunkMax), chunker.WithAverageBits(chunkAvgBits))
	buffer := make([]byte, chunkMax)
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderCRC(true))
	if err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	defer encoder.Close()
	var count uint64
	var offset int64
	for {
		chunk, nextErr := c.Next(buffer)
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			cryptox.Zero(key)
			return "", nil, nextErr
		}
		if chunk.Length == 0 || chunk.Length > chunkMax || int64(chunk.Start) != offset {
			cryptox.Zero(key)
			return "", nil, ErrFormat
		}
		chunkKey, objectID, chunkErr := s.writeChunk(ctx, op, id, encoder, chunk.Data)
		if chunkErr != nil {
			cryptox.Zero(key)
			return "", nil, chunkErr
		}
		chunkErr = writeLine(manifestLine{Type: "chunk", ObjectID: objectID, Key: cryptox.B64(chunkKey), Offset: offset, Length: int64(chunk.Length)})
		cryptox.Zero(chunkKey)
		if chunkErr != nil {
			cryptox.Zero(key)
			return "", nil, chunkErr
		}
		offset += int64(chunk.Length)
		count++
	}
	if offset != length || !equalBytes(hash.Sum(nil), contentHash) {
		cryptox.Zero(key)
		return "", nil, ErrState
	}
	if err = writeLine(manifestLine{Type: "footer", Length: length, Count: count}); err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	src, err := plain.Open(ctx)
	if err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	cipherFile, err := os.CreateTemp(filepath.Join(s.root, "shared-staging"), "manifest-cipher-")
	if err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	cipherPath := cipherFile.Name()
	defer os.Remove(cipherPath)
	if err = cipherFile.Chmod(0o600); err == nil {
		err = encryptFile(src, cipherFile, id, key)
	}
	if err == nil {
		err = cipherFile.Sync()
	}
	if closeErr := cipherFile.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(cipherPath, s.objectPath(id))
	}
	if err == nil {
		err = syncDir(filepath.Dir(s.objectPath(id)))
	}
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE objects SET state='ready' WHERE object_id=? AND state='building'`, id)
	}
	if err != nil {
		cryptox.Zero(key)
		return "", nil, err
	}
	ready = true
	return id, key, nil
}

func (s *Store) removeClaimedObject(id string) error {
	path := s.objectPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM objects WHERE object_id=? AND state='deleting'`, id)
	return err
}

func (s *Store) writeChunk(ctx context.Context, op, parent string, encoder *zstd.Encoder, data []byte) ([]byte, string, error) {
	if len(data) == 0 || len(data) > chunkMax {
		return nil, "", ErrFormat
	}
	encoded, encoding, err := encodeChunkWith(encoder, data)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(data)
	fingerprint := s.keys.chunkFingerprint(int64(len(data)), hash[:])
	id, key, err := s.getOrWriteObject(ctx, bytes.NewReader(encoded), fingerprint, int64(len(data)), int64(len(encoded)), encoding, "chunk", parent, op)
	return key, id, err
}
