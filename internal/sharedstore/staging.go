package sharedstore

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

// encryptedStage keeps its random key only in memory; abandoned files cannot
// reveal source bytes or manifest chunk keys after a process exit.
type encryptedStage struct {
	file   *os.File
	writer *cryptox.StreamFileWriter
	reader *cryptox.StreamFileReader
	key    []byte
}

func (s *Store) newEncryptedStage(prefix string) (*encryptedStage, error) {
	key, err := cryptox.Random(32)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(filepath.Join(s.root, "shared-staging"), prefix)
	if err != nil {
		cryptox.Zero(key)
		return nil, err
	}
	stage := &encryptedStage{file: file, key: key}
	if err = file.Chmod(0o600); err == nil {
		stage.writer, err = cryptox.NewStreamFileWriter(file, key)
	}
	if err != nil {
		stage.Close()
		return nil, err
	}
	return stage, nil
}

func (s *encryptedStage) Write(p []byte) (int, error) { return s.writer.Write(p) }

func (s *encryptedStage) Open(ctx context.Context) (io.ReadSeeker, error) {
	if err := s.writer.Close(); err != nil {
		return nil, err
	}
	if err := s.file.Sync(); err != nil {
		return nil, err
	}
	if err := s.file.Close(); err != nil {
		return nil, err
	}
	reader, err := cryptox.OpenStreamFile(ctx, s.file.Name(), s.key, s.writer.Size())
	if err != nil {
		return nil, err
	}
	s.reader = reader
	return reader, nil
}

func (s *encryptedStage) Close() {
	if s.reader != nil {
		_ = s.reader.Close()
	}
	if s.writer != nil {
		_ = s.writer.Close()
	}
	_ = s.file.Close()
	_ = os.Remove(s.file.Name())
	cryptox.Zero(s.key)
}
