package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"time"

	"github.com/restic/restic/internal/fs"
)

// Only successful, verified EOF may reach the archiver. In particular a short
// source must never be interpreted as a valid shorter file.
type checkedSource struct {
	io.ReadCloser
	remaining int64
	want      string
	hash      hash.Hash
	verified  bool
	cancel    context.CancelFunc
}

func (s *checkedSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.verified {
		return 0, io.EOF
	}
	// Read at most remaining+1 without overflowing int64. The extra byte
	// detects overlong streams; EOF is still required at the exact length.
	if s.remaining < int64(len(p)) {
		p = p[:int(s.remaining)+1]
	}
	n, err := s.ReadCloser.Read(p)
	if int64(n) > s.remaining {
		s.cancel()
		return 0, errBatch
	}
	s.remaining -= int64(n)
	_, _ = s.hash.Write(p[:n])
	if err == io.EOF {
		if s.remaining != 0 || hex.EncodeToString(s.hash.Sum(nil)) != s.want {
			s.cancel()
			return 0, errBatch
		}
		s.verified = true
	} else if err != nil {
		s.cancel()
		return 0, errBatch
	}
	return n, err
}

func (s *checkedSource) Close() error {
	err := s.ReadCloser.Close()
	if err != nil || !s.verified {
		s.cancel()
		return errBatch
	}
	return nil
}

// Reader supplies Restic's synthetic metadata and single-open stream handling;
// routing selects the independent Reader for each explicit root-level target.
// No operation falls back to the host filesystem.
type batchFS struct {
	*fs.Reader
	files map[string]*fs.Reader
}

func newBatchFS(m manifest, pipes []*os.File, cancel context.CancelFunc, now time.Time) (*batchFS, []string) {
	b := &batchFS{Reader: &fs.Reader{}, files: make(map[string]*fs.Reader)}
	targets := make([]string, 0, len(m.Files))
	for i, e := range m.Files {
		name := "/" + e.Name
		source := &checkedSource{ReadCloser: pipes[i], remaining: *e.Size, want: e.SHA256, hash: sha256.New(), cancel: cancel}
		b.files[name] = &fs.Reader{Name: name, ReadCloser: source, Mode: 0600, ModTime: now, Size: *e.Size, AllowEmptyFile: true}
		targets = append(targets, name)
	}
	return b, targets
}

func (b *batchFS) OpenFile(name string, flag int, metadataOnly bool) (fs.File, error) {
	if r := b.files[name]; r != nil {
		return r.OpenFile(name, flag, metadataOnly)
	}
	return nil, errBatch
}

func (b *batchFS) Lstat(name string) (*fs.ExtendedFileInfo, error) {
	if r := b.files[name]; r != nil {
		return r.Lstat(name)
	}
	if name == "/" || name == "." {
		return b.Reader.Lstat(name)
	}
	return nil, errBatch
}
