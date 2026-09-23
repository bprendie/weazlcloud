package sharedstore

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"io"
	"os"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

var objectMagic = []byte("WZLOBJ01")

func encryptFile(src, dst *os.File, objectID string, key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	prefix, err := cryptox.Random(4)
	if err != nil {
		return err
	}
	header := append(append([]byte(nil), objectMagic...), prefix...)
	if err = writeAll(dst, header); err != nil {
		return err
	}
	buf := make([]byte, frameSize)
	var seq uint64
	var total int64
	for {
		n, readErr := io.ReadFull(src, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		if n > 0 {
			record := make([]byte, 13)
			record[0] = 1
			binary.BigEndian.PutUint64(record[1:9], seq)
			binary.BigEndian.PutUint32(record[9:13], uint32(n))
			sealed := gcm.Seal(nil, nonce(prefix, seq), buf[:n], aad(header, record, objectID))
			if err = writeAll(dst, record); err != nil {
				return err
			}
			if err = writeAll(dst, sealed); err != nil {
				return err
			}
			seq++
			total += int64(n)
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	record := make([]byte, 13)
	record[0] = 2
	binary.BigEndian.PutUint64(record[1:9], seq)
	final := make([]byte, 16)
	binary.BigEndian.PutUint64(final[:8], uint64(total))
	binary.BigEndian.PutUint64(final[8:], seq)
	sealed := gcm.Seal(nil, nonce(prefix, seq), final, aad(header, record, objectID))
	if err = writeAll(dst, record); err != nil {
		return err
	}
	return writeAll(dst, sealed)
}

func decryptFile(src io.Reader, dst io.Writer, objectID string, key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	header := make([]byte, 12)
	if _, err = io.ReadFull(src, header); err != nil {
		return ErrFormat
	}
	if string(header[:8]) != string(objectMagic) {
		return ErrFormat
	}
	prefix := header[8:]
	var seq uint64
	var total int64
	for {
		record := make([]byte, 13)
		if _, err = io.ReadFull(src, record); err != nil {
			return ErrFormat
		}
		gotSeq := binary.BigEndian.Uint64(record[1:9])
		if gotSeq != seq {
			return ErrFormat
		}
		size := binary.BigEndian.Uint32(record[9:13])
		switch record[0] {
		case 1:
			if size == 0 || size > frameSize {
				return ErrFormat
			}
			sealed := make([]byte, int(size)+gcm.Overhead())
			if _, err = io.ReadFull(src, sealed); err != nil {
				return ErrFormat
			}
			plain, openErr := gcm.Open(nil, nonce(prefix, seq), sealed, aad(header, record, objectID))
			if openErr != nil {
				return ErrFormat
			}
			if err = writeAll(dst, plain); err != nil {
				cryptox.Zero(plain)
				return err
			}
			total += int64(len(plain))
			cryptox.Zero(plain)
			seq++
		case 2:
			if size != 0 {
				return ErrFormat
			}
			sealed := make([]byte, 16+gcm.Overhead())
			if _, err = io.ReadFull(src, sealed); err != nil {
				return ErrFormat
			}
			final, openErr := gcm.Open(nil, nonce(prefix, seq), sealed, aad(header, record, objectID))
			if openErr != nil || len(final) != 16 || binary.BigEndian.Uint64(final[:8]) != uint64(total) || binary.BigEndian.Uint64(final[8:]) != seq {
				return ErrFormat
			}
			var extra [1]byte
			for {
				n, readErr := src.Read(extra[:])
				if n != 0 {
					return ErrFormat
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					return ErrFormat
				}
			}
			return nil
		default:
			return ErrFormat
		}
	}
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

func nonce(prefix []byte, seq uint64) []byte {
	n := make([]byte, 12)
	copy(n, prefix)
	binary.BigEndian.PutUint64(n[4:], seq)
	return n
}

func aad(header, record []byte, objectID string) []byte {
	a := make([]byte, 0, len(header)+len(record)+len(objectID))
	a = append(a, header...)
	a = append(a, record...)
	return append(a, objectID...)
}
