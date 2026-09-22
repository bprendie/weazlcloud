package library

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/restic"
	"github.com/bprendie/weazlcloud/internal/vault"
)

var ErrArchiveSelectionEmpty = errors.New("archive selection is empty")

// ArchiveEntry is the immutable catalog/restic reference captured for one ZIP
// job. Keeping the snapshot and object IDs here means a later move or replace
// cannot change the contents of a queued archive.
type ArchiveEntry struct {
	Path   string
	Folder bool
	Size   int64
	Mtime  time.Time
	Snap   string
	Object string
	Hash   string
}

type ArchiveManifest struct {
	Entries []ArchiveEntry
	Files   int
	Bytes   int64
}

// PrepareArchive captures the selected catalog entries while holding the same
// library lock used by normal reads. It does not read file contents.
func (l *Library) PrepareArchive(ctx context.Context, selections []string) (ArchiveManifest, error) {
	cleanSelections := make([]string, 0, len(selections))
	seenSelections := make(map[string]struct{})
	for _, path := range selections {
		path, err := cleanPath(path)
		if err != nil {
			return ArchiveManifest{}, err
		}
		if path == "" {
			return ArchiveManifest{}, ErrArchiveSelectionEmpty
		}
		if _, ok := seenSelections[path]; ok {
			continue
		}
		seenSelections[path] = struct{}{}
		cleanSelections = append(cleanSelections, path)
	}
	if len(cleanSelections) == 0 {
		return ArchiveManifest{}, ErrArchiveSelectionEmpty
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return ArchiveManifest{}, err
	}
	entries := l.catalog.List()
	files := make(map[string]ArchiveEntry)
	dirs := make(map[string]time.Time)
	for _, selection := range cleanSelections {
		for _, entry := range entries {
			if entry.Path != selection && !strings.HasPrefix(entry.Path, selection+"/") {
				continue
			}
			if entry.Folder {
				dirs[entry.Path] = entry.Mtime
			} else {
				files[entry.Path] = ArchiveEntry{Path: entry.Path, Size: entry.Size, Mtime: entry.Mtime, Snap: entry.Snap, Object: entry.Object, Hash: entry.Hash}
			}
		}
	}
	if len(files) == 0 && len(dirs) == 0 {
		return ArchiveManifest{}, errors.New("archive selection is not in the library")
	}
	for path, entry := range files {
		for parent := filepath.ToSlash(filepath.Dir(path)); parent != "." && parent != ""; parent = filepath.ToSlash(filepath.Dir(parent)) {
			if _, ok := dirs[parent]; !ok {
				dirs[parent] = entry.Mtime
			}
		}
	}

	dirNames := make([]string, 0, len(dirs))
	for path := range dirs {
		dirNames = append(dirNames, path)
	}
	sort.Strings(dirNames)
	fileNames := make([]string, 0, len(files))
	for path := range files {
		fileNames = append(fileNames, path)
	}
	sort.Strings(fileNames)
	manifest := ArchiveManifest{Entries: make([]ArchiveEntry, 0, len(dirNames)+len(fileNames)), Files: len(fileNames)}
	for _, path := range dirNames {
		manifest.Entries = append(manifest.Entries, ArchiveEntry{Path: path, Folder: true, Mtime: dirs[path]})
	}
	for _, path := range fileNames {
		entry := files[path]
		manifest.Entries = append(manifest.Entries, entry)
		manifest.Bytes += entry.Size
	}
	return manifest, nil
}

// WriteArchive streams a captured manifest into a ZIP writer. The ZIP writer
// handles ZIP64 without buffering file data.
func (l *Library) WriteArchive(ctx context.Context, manifest ArchiveManifest, w io.Writer) (int, int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return 0, 0, err
	}
	pass, _, err := l.vault.Secrets()
	if err != nil {
		return 0, 0, vault.ErrLocked
	}
	archive := zip.NewWriter(w)
	var files int
	var total int64
	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			_ = archive.Close()
			return files, total, err
		}
		name := entry.Path
		if entry.Folder {
			name += "/"
		}
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetModTime(entry.Mtime)
		out, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			return files, total, err
		}
		if entry.Folder {
			continue
		}
		object := entry.Object
		if object == "" {
			object = entry.Hash
		}
		if err := l.restic.Dump(ctx, restic.Repo{Location: l.repo, Password: pass}, entry.Snap, object, out); err != nil {
			_ = archive.Close()
			return files, total, err
		}
		files++
		total += entry.Size
	}
	if err := archive.Close(); err != nil {
		return files, total, err
	}
	return files, total, nil
}
