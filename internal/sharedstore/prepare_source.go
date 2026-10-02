package sharedstore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
)

func (s *Store) prepareSource(ctx context.Context, input io.Reader, expected int64, seekable bool) (io.Reader, int64, []byte, func(), error) {
	cleanup := func() {}
	var start int64
	var reader io.ReadSeeker
	var dst io.Writer = io.Discard
	var stage *encryptedStage
	var err error
	if seekable {
		reader = input.(io.ReadSeeker)
		start, err = reader.Seek(0, io.SeekCurrent)
	} else {
		stage, err = s.newEncryptedStage("source-")
		if err == nil {
			cleanup, dst = stage.Close, stage
		}
	}
	if err != nil {
		return nil, 0, nil, nil, err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, hash), contextReader{ctx, input})
	if err == nil && expected >= 0 && n != expected {
		err = fmt.Errorf("source length mismatch: got %d", n)
	}
	if err == nil {
		if seekable {
			_, err = reader.Seek(start, io.SeekStart)
		} else {
			reader, err = stage.Open(ctx)
		}
	}
	if err != nil {
		cleanup()
		return nil, 0, nil, nil, err
	}
	return reader, n, hash.Sum(nil), cleanup, nil
}
