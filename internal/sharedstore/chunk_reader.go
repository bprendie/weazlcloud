package sharedstore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type manifestReader struct {
	s        *Store
	ctx      context.Context
	buffer   []byte
	started  bool
	finished bool
	count    uint64
	offset   int64
	wantSize int64
	dst      io.Writer
}

func (m *manifestReader) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		next := bytes.IndexByte(p, '\n')
		if next < 0 {
			if len(m.buffer)+len(p) > 4096 {
				return 0, ErrFormat
			}
			m.buffer = append(m.buffer, p...)
			break
		}
		if len(m.buffer)+next > 4096 {
			return 0, ErrFormat
		}
		m.buffer = append(m.buffer, p[:next]...)
		if err := m.line(m.buffer); err != nil {
			return 0, err
		}
		m.buffer = m.buffer[:0]
		p = p[next+1:]
	}
	return written, nil
}

func (m *manifestReader) line(raw []byte) error {
	var line manifestLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return ErrFormat
	}
	if !m.started {
		if line.Type != "header" || line.Version != chunkFormatVersion || line.Chunker != chunkSettings {
			return ErrFormat
		}
		m.started = true
		return nil
	}
	if m.finished {
		return ErrFormat
	}
	switch line.Type {
	case "chunk":
		if !validObjectID(line.ObjectID) || line.Offset != m.offset || line.Length <= 0 || line.Length > chunkMax {
			return ErrFormat
		}
		key, err := cryptox.B64d(line.Key)
		if err != nil || len(key) != 32 {
			return ErrFormat
		}
		defer cryptox.Zero(key)
		if err = m.s.readChunk(m.ctx, line.ObjectID, key, line.Length, m.dst); err != nil {
			return err
		}
		m.offset += line.Length
		m.count++
	case "footer":
		if line.Length != m.offset || line.Length != m.wantSize || line.Count != m.count {
			return ErrFormat
		}
		m.finished = true
	default:
		return ErrFormat
	}
	return nil
}

func (m *manifestReader) finish() error {
	if len(m.buffer) != 0 || !m.started || !m.finished {
		return ErrFormat
	}
	return nil
}

func (s *Store) readChunk(ctx context.Context, id string, key []byte, length int64, dst io.Writer) error {
	var wrapped []byte
	var plainLength, storedLength int64
	var state, kind, encoding string
	if err := s.db.QueryRowContext(ctx, `SELECT node_key,plain_len,stored_len,state,kind,encoding FROM objects WHERE object_id=?`, id).Scan(&wrapped, &plainLength, &storedLength, &state, &kind, &encoding); err != nil {
		return ErrState
	}
	if state != "ready" || kind != "chunk" || plainLength != length || storedLength <= 0 {
		return ErrState
	}
	nodeKey, err := s.unwrapNodeKey(wrapped)
	if err != nil {
		return ErrState
	}
	defer cryptox.Zero(nodeKey)
	if !equalBytes(nodeKey, key) {
		return ErrState
	}
	file, err := os.Open(s.objectPath(id))
	if err != nil {
		return ErrState
	}
	defer file.Close()
	var stored bytes.Buffer
	if err = decryptFile(file, &stored, id, key); err != nil {
		return err
	}
	if int64(stored.Len()) != storedLength {
		return ErrState
	}
	_, err = decodeChunk(encoding, stored.Bytes(), length, contextWriter{ctx, dst})
	return err
}

func (s *Store) readManifest(ctx context.Context, id string, key []byte, size int64, dst io.Writer) error {
	file, err := os.Open(s.objectPath(id))
	if err != nil {
		return ErrState
	}
	defer file.Close()
	parser := &manifestReader{s: s, ctx: ctx, dst: contextWriter{ctx, dst}, wantSize: size}
	if err = decryptFile(file, parser, id, key); err != nil {
		return err
	}
	return parser.finish()
}
