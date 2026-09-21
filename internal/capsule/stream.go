package capsule

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

const (
	streamMagic = "WZC2"
	streamChunk = 1 << 20
)

// StreamSource produces plaintext into the capsule writer. It is called once.
type StreamSource func(io.Writer) error

// MintStream stores authenticated chunks without building the payload in RAM.
// Mint continues to write the legacy one-shot format for compatibility tests
// and any callers that still provide an in-memory payload.
func (s *Store) MintStream(rec Record, phrase string, source StreamSource) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == nil {
		return Record{}, errors.New("capsule source is nil")
	}
	id, err := cryptox.Random(16)
	if err != nil {
		return Record{}, err
	}
	rec.ID = encodeToken(id)
	key, err := cryptox.Random(cryptox.KeyBytes)
	if err != nil {
		return Record{}, err
	}
	defer cryptox.Zero(key)
	dir := filepath.Join(s.root, rec.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Record{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	tmp, err := os.CreateTemp(dir, ".payload-*")
	if err != nil {
		return Record{}, err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return Record{}, err
	}
	count, err := encryptStream(tmp, key, source)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpName)
		return Record{}, err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, "payload")); err != nil {
		os.Remove(tmpName)
		return Record{}, err
	}
	rec.Size = count
	if rec.Gate == "passphrase" {
		if phrase == "" {
			return Record{}, ErrPhrase
		}
		if err := wrapKey(dir, key, []byte(phrase)); err != nil {
			return Record{}, err
		}
	} else {
		rec.Gate = "open"
		if err := cryptox.AtomicWrite(filepath.Join(dir, "open.key"), key, 0o600); err != nil {
			return Record{}, err
		}
	}
	if err := writeMeta(dir, rec); err != nil {
		return Record{}, err
	}
	committed = true
	return rec, nil
}

func encryptStream(dst io.Writer, key []byte, source StreamSource) (int64, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return 0, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return 0, err
	}
	if _, err := io.WriteString(dst, streamMagic); err != nil {
		return 0, err
	}
	var count countingWriter
	count.w = &streamEncryptWriter{dst: dst, gcm: gcm}
	if err := source(&count); err != nil {
		return count.n, err
	}
	if err := count.w.(*streamEncryptWriter).finish(); err != nil {
		return count.n, err
	}
	return count.n, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

type streamEncryptWriter struct {
	dst io.Writer
	gcm cipher.AEAD
	buf []byte
}

func (w *streamEncryptWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := len(p)
		if n > streamChunk {
			n = streamChunk
		}
		part := p[:n]
		nonce := make([]byte, w.gcm.NonceSize())
		if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
			return total, err
		}
		if err := writeUint32(w.dst, uint32(n)); err != nil {
			return total, err
		}
		if _, err := w.dst.Write(nonce); err != nil {
			return total, err
		}
		if _, err := w.dst.Write(w.gcm.Seal(w.buf[:0], nonce, part, nil)); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (w *streamEncryptWriter) finish() error { return writeUint32(w.dst, 0) }

func writeUint32(w io.Writer, n uint32) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	_, err := w.Write(b[:])
	return err
}

func decryptStream(src io.Reader, key []byte, dst io.Writer) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return ErrGone
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ErrGone
	}
	for {
		var length [4]byte
		if _, err := io.ReadFull(src, length[:]); err != nil {
			return ErrGone
		}
		n := binary.BigEndian.Uint32(length[:])
		if n == 0 {
			return nil
		}
		if n > streamChunk {
			return ErrGone
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err := io.ReadFull(src, nonce); err != nil {
			return ErrGone
		}
		ciphertext := make([]byte, int(n)+gcm.Overhead())
		if _, err := io.ReadFull(src, ciphertext); err != nil {
			return ErrGone
		}
		plain, err := gcm.Open(nil, nonce, ciphertext, nil)
		if err != nil {
			return ErrGone
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
	}
}

// StreamGrab authenticates and consumes a grab, then lets the caller provide
// the response writer after the remaining-grab count is known.
func (s *Store) StreamGrab(id, phrase string, open func(Record) (io.Writer, error)) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if open == nil {
		return Record{}, errors.New("grab writer is nil")
	}
	dir := filepath.Join(s.root, id)
	rec, err := readMeta(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Record{}, ErrGone
		}
		return Record{}, err
	}
	if !rec.Live() {
		return rec, ErrGone
	}
	key, err := loadKey(dir, rec.Gate, phrase)
	if err != nil {
		return rec, err
	}
	defer cryptox.Zero(key)
	payload, err := os.Open(filepath.Join(dir, "payload"))
	if err != nil {
		return rec, ErrGone
	}
	defer payload.Close()
	var magic [len(streamMagic)]byte
	if _, err := io.ReadFull(payload, magic[:]); err != nil {
		return rec, ErrGone
	}
	if string(magic[:]) != streamMagic {
		plain, rec, err := s.grabLocked(id, phrase)
		if err != nil {
			return rec, err
		}
		w, err := open(rec)
		if err == nil {
			_, err = w.Write(plain)
		}
		cryptox.Zero(plain)
		return rec, err
	}
	rec.Used++
	terminal := rec.Used >= rec.Limit
	if terminal {
		rec.Revoked = true
	}
	if err := writeMeta(dir, rec); err != nil {
		return rec, ErrStorage
	}
	cleanup := func() error {
		if !terminal {
			return nil
		}
		for _, name := range []string{"open.key", "pass.wrap", "payload"} {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	w, err := open(rec)
	if err != nil {
		_ = cleanup()
		return rec, err
	}
	if err := decryptStream(payload, key, w); err != nil {
		_ = cleanup()
		return rec, err
	}
	if err := cleanup(); err != nil {
		return rec, ErrStorage
	}
	return rec, nil
}
