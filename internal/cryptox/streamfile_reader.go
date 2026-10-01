package cryptox

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"io"
	"math"
	"os"
)

var ErrStreamFile = errors.New("encrypted archive is incomplete or corrupt")

// StreamFileReader supports HTTP byte ranges with at most one decrypted chunk
// retained. Chunks are bound to their position, preventing reorder/substitution.
type StreamFileReader struct {
	file                       *os.File
	ctx                        context.Context
	gcm                        cipher.AEAD
	size, position, cacheIndex int64
	cache                      []byte
}

func OpenStreamFile(ctx context.Context, name string, key []byte, size int64) (*StreamFileReader, error) {
	if size < 0 {
		return nil, ErrStreamFile
	}
	blocks := size / streamFileChunk
	if size%streamFileChunk != 0 {
		blocks++
	}
	if blocks > (math.MaxInt64-size-4)/streamFileOverhead {
		return nil, ErrStreamFile
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*StreamFileReader, error) { f.Close(); return nil, err }
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if info.Size() != 4+size+blocks*streamFileOverhead {
		return fail(ErrStreamFile)
	}
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != streamFileMagic {
		return fail(ErrStreamFile)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fail(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fail(err)
	}
	return &StreamFileReader{file: f, ctx: ctx, gcm: gcm, size: size, cacheIndex: -1}, nil
}

func (r *StreamFileReader) Read(body []byte) (int, error) {
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.position >= r.size {
		return 0, io.EOF
	}
	if len(body) == 0 {
		return 0, nil
	}
	index := r.position / streamFileChunk
	if index != r.cacheIndex {
		clear(r.cache)
		r.cache = nil
		length := min(int64(streamFileChunk), r.size-index*streamFileChunk)
		frame := make([]byte, length+streamFileOverhead)
		if _, err := r.file.ReadAt(frame, 4+index*(streamFileChunk+streamFileOverhead)); err != nil {
			return 0, ErrStreamFile
		}
		plain, err := r.gcm.Open(nil, frame[:r.gcm.NonceSize()], frame[r.gcm.NonceSize():], streamFileAAD(uint64(index)))
		if err != nil {
			return 0, ErrStreamFile
		}
		r.cache, r.cacheIndex = plain, index
	}
	n := copy(body, r.cache[r.position%streamFileChunk:])
	r.position += int64(n)
	return n, nil
}

func (r *StreamFileReader) Seek(offset int64, whence int) (int64, error) {
	if r.file == nil {
		return 0, os.ErrClosed
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = r.position
	case io.SeekEnd:
		base = r.size
	default:
		return 0, ErrStreamFile
	}
	if offset < -base || offset > math.MaxInt64-base {
		return 0, ErrStreamFile
	}
	r.position = base + offset
	return r.position, nil
}

func (r *StreamFileReader) Close() error {
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	r.gcm = nil
	clear(r.cache)
	r.cache = nil
	return err
}
