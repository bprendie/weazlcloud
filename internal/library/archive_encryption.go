package library

import (
	"context"
	"encoding/hex"
	"io"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (l *Library) SealArchiveMetadata(plain []byte) ([]byte, error) { return l.vault.Wrap(plain) }
func (l *Library) OpenArchiveMetadata(raw []byte) ([]byte, error)   { return l.vault.Unwrap(raw) }

func (m ArchiveManifest) PhotoOnly() bool {
	if len(m.Entries) == 0 {
		return false
	}
	for _, entry := range m.Entries {
		if !inPhotoRoot(entry.Path) || !entry.Folder && !photoMedia(entry.Path) || strings.HasPrefix(entry.Path, ".weazl-") {
			return false
		}
	}
	return true
}

func (l *Library) archiveKey(id string) ([]byte, error) {
	bytes, err := hex.DecodeString(id)
	if err != nil || len(bytes) != 16 {
		return nil, ErrArchiveSelectionEmpty
	}
	return l.vault.Fingerprint("private-archive", bytes)
}

func (l *Library) WriteEncryptedArchive(ctx context.Context, id string, manifest ArchiveManifest, dst io.Writer) (int, int64, error) {
	key, err := l.archiveKey(id)
	if err != nil {
		manifest.Release()
		return 0, 0, err
	}
	defer clear(key)
	writer, err := cryptox.NewStreamFileWriter(dst, key)
	if err != nil {
		manifest.Release()
		return 0, 0, err
	}
	files, _, err := l.WriteArchive(ctx, manifest, writer)
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	return files, writer.Size(), err
}

type archiveReader struct {
	io.ReadSeekCloser
	done func()
}

func (r *archiveReader) Close() error { defer r.done(); return r.ReadSeekCloser.Close() }

func (l *Library) OpenEncryptedArchive(ctx context.Context, id, path string, size int64) (io.ReadSeekCloser, error) {
	ctx, done := l.previewContext(ctx)
	key, err := l.archiveKey(id)
	if err != nil {
		done()
		return nil, err
	}
	defer clear(key)
	reader, err := cryptox.OpenStreamFile(ctx, path, key, size)
	if err != nil {
		done()
		return nil, err
	}
	return &archiveReader{ReadSeekCloser: reader, done: done}, nil
}

func (l *Library) HoldArchiveManifest(ctx context.Context, manifest ArchiveManifest) (ArchiveManifest, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return ArchiveManifest{}, err
	}
	manifest.holds = &archiveHolds{}
	for _, entry := range manifest.Entries {
		if entry.Folder {
			continue
		}
		release, err := l.holdReference(entry.Ref)
		if err != nil {
			manifest.Release()
			return ArchiveManifest{}, err
		}
		manifest.holds.releases = append(manifest.holds.releases, release)
	}
	return manifest, nil
}
