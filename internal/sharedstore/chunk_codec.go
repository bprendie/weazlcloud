package sharedstore

import (
	"bytes"
	"io"

	"github.com/klauspost/compress/zstd"
)

const (
	chunkRaw  = "raw"
	chunkZstd = "zstd-v1"
)

func encodeChunk(plain []byte) ([]byte, string, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderCRC(true))
	if err != nil {
		return nil, "", err
	}
	compressed := encoder.EncodeAll(plain, nil)
	encoder.Close()
	if len(compressed)+64 >= len(plain) {
		return plain, chunkRaw, nil
	}
	return compressed, chunkZstd, nil
}

func decodeChunk(encoding string, stored []byte, expected int64, dst io.Writer) (int64, error) {
	var reader io.Reader = bytes.NewReader(stored)
	var decoder *zstd.Decoder
	if encoding == chunkZstd {
		var err error
		decoder, err = zstd.NewReader(reader, zstd.WithDecoderMaxMemory(16<<20), zstd.WithDecoderMaxWindow(8<<20))
		if err != nil {
			return 0, ErrFormat
		}
		defer decoder.Close()
		reader = decoder
	} else if encoding != chunkRaw {
		return 0, ErrFormat
	}
	counted := &countWriter{w: dst}
	if _, err := io.Copy(counted, reader); err != nil {
		return counted.n, ErrFormat
	}
	if counted.n != expected {
		return counted.n, ErrFormat
	}
	return counted.n, nil
}
