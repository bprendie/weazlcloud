package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"io"
)

const streamFileMagic = "WZA1"
const streamFileChunk = 1 << 20
const streamFileOverhead = 28 // 12-byte nonce + 16-byte authentication tag

// StreamFileWriter coalesces arbitrary ZIP writes into authenticated, indexed
// fixed-size chunks. The plaintext length lives in separately sealed metadata.
type StreamFileWriter struct {
	dst    io.Writer
	gcm    cipher.AEAD
	buffer []byte
	index  uint64
	size   int64
	err    error
	closed bool
}

func NewStreamFileWriter(dst io.Writer, key []byte) (*StreamFileWriter, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if n, err := io.WriteString(dst, streamFileMagic); err != nil {
		return nil, err
	} else if n != len(streamFileMagic) {
		return nil, io.ErrShortWrite
	}
	return &StreamFileWriter{dst: dst, gcm: gcm, buffer: make([]byte, 0, streamFileChunk)}, nil
}

func streamFileAAD(index uint64) []byte {
	ad := make([]byte, 12)
	copy(ad, streamFileMagic)
	binary.BigEndian.PutUint64(ad[4:], index)
	return ad
}

func (w *StreamFileWriter) Write(body []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if w.err != nil {
		return 0, w.err
	}
	written := 0
	for len(body) != 0 {
		n := min(len(body), streamFileChunk-len(w.buffer))
		w.buffer = append(w.buffer, body[:n]...)
		body = body[n:]
		written += n
		w.size += int64(n)
		if len(w.buffer) == streamFileChunk {
			if err := w.flush(); err != nil {
				w.err = err
				return written, err
			}
		}
	}
	return written, nil
}

func (w *StreamFileWriter) flush() error {
	if len(w.buffer) == 0 {
		return nil
	}
	nonce := make([]byte, w.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	frame := w.gcm.Seal(nonce, nonce, w.buffer, streamFileAAD(w.index))
	n, err := w.dst.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	clear(w.buffer)
	w.buffer = w.buffer[:0]
	w.index++
	return err
}

func (w *StreamFileWriter) Close() error {
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err == nil {
		w.err = w.flush()
	}
	clear(w.buffer)
	w.buffer = nil
	w.gcm = nil
	w.dst = nil
	return w.err
}
func (w *StreamFileWriter) Size() int64 { return w.size }
