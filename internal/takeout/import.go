package takeout

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

type Summary struct {
	Archive        string `json:"archive"`
	Files          int    `json:"files"`
	Folders        int    `json:"folders"`
	Bytes          uint64 `json:"bytes"`
	Largest        uint64 `json:"largest"`
	Imported       int    `json:"imported"`
	Skipped        int    `json:"skipped"`
	ProcessedBytes uint64 `json:"processed_bytes"`
}

// Destination groups Google products even when one ZIP contains both of them.
// Keep unknown products instead of silently dropping export data.
func Destination(prefix, raw string) (string, error) {
	clean, err := cleanEntry(raw)
	if err != nil {
		return "", err
	}
	parts := strings.Split(clean, "/")
	if len(parts) > 1 && strings.EqualFold(parts[0], "Takeout") {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "", errors.New("empty Takeout path")
	}
	group := "Other"
	switch strings.ToLower(parts[0]) {
	case "drive", "google drive":
		group = "Drive"
		parts = parts[1:]
	case "google photos", "photos":
		group = "Photos"
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return prefix + "/" + group, nil
	}
	return library.CleanPath(prefix + "/" + group + "/" + strings.Join(parts, "/"))
}

// Open accepts one regular, completed ZIP from a fixed staging directory.
// The directory is supplied by the node, never by an HTTP request.
func Open(root, name string) (*os.File, *zip.Reader, error) {
	if root == "" || name == "" || path.Base(name) != name || strings.ContainsAny(name, "\\\x00") || !strings.EqualFold(path.Ext(name), ".zip") {
		return nil, nil, errors.New("invalid staged ZIP name")
	}
	location := path.Join(root, name)
	info, err := os.Lstat(location)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("staged ZIP must be a regular file")
	}
	f, err := os.Open(location)
	if err != nil {
		return nil, nil, err
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, z, nil
}

func cleanEntry(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") {
		return "", errors.New("unsafe ZIP path")
	}
	name = strings.TrimSuffix(name, "/")
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", errors.New("unsafe ZIP path")
		}
	}
	return library.CleanPath(name)
}

func Scan(name string, z *zip.Reader, prefix string) (Summary, error) {
	if _, err := library.CleanPath(prefix); err != nil {
		return Summary{}, err
	}
	s := Summary{Archive: name}
	for _, entry := range z.File {
		if _, err := Destination(prefix, entry.Name); err != nil {
			return Summary{}, fmt.Errorf("%s: %w", entry.Name, err)
		}
		mode := entry.Mode()
		if !(mode.IsRegular() || mode.IsDir()) {
			return Summary{}, fmt.Errorf("%s: special file not allowed", entry.Name)
		}
		if mode.IsDir() {
			s.Folders++
			continue
		}
		if s.Bytes > ^uint64(0)-entry.UncompressedSize64 {
			return Summary{}, errors.New("ZIP size overflow")
		}
		s.Files++
		s.Bytes += entry.UncompressedSize64
		if entry.UncompressedSize64 > s.Largest {
			s.Largest = entry.UncompressedSize64
		}
	}
	return s, nil
}

type Reserve func(int64) (func(), error)

// Import is restartable: committed catalog hashes are checked before writing.
// A conflicting path fails closed, leaving the staged ZIP untouched.
func Import(ctx context.Context, lib *library.Library, name string, z *zip.Reader, prefix string, reserve Reserve, progress func(Summary)) (Summary, error) {
	s, err := Scan(name, z, prefix)
	if err != nil {
		return s, err
	}
	if err := lib.Ensure(ctx); err != nil {
		return s, err
	}
	existing := make(map[string]catalog.File)
	for _, file := range lib.List() {
		existing[file.Path] = file
	}
	if err := ensureFolder(ctx, lib, existing, prefix); err != nil {
		return s, err
	}
	for _, entry := range z.File {
		if err := ctx.Err(); err != nil {
			return s, err
		}
		target, _ := Destination(prefix, entry.Name) // Scan validated every name.
		if err := ensureParents(ctx, lib, existing, target); err != nil {
			return s, fmt.Errorf("%s: %w", target, err)
		}
		if entry.FileInfo().IsDir() {
			if err := ensureFolder(ctx, lib, existing, target); err != nil {
				return s, fmt.Errorf("%s: %w", target, err)
			}
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return s, fmt.Errorf("%s: %w", target, err)
		}
		var albumRaw bytes.Buffer
		var body io.Reader = reader
		albumMetadata := library.IsPhotoAlbumMetadata(target) && entry.UncompressedSize64 <= 1<<20
		if albumMetadata {
			body = io.TeeReader(reader, &albumRaw)
		}
		previous, found := existing[target]
		if found {
			if previous.Folder || previous.Size < 0 || uint64(previous.Size) != entry.UncompressedSize64 {
				reader.Close()
				return s, fmt.Errorf("%s: conflicting library path", target)
			}
			hash := sha256.New()
			_, err = io.Copy(hash, body)
			closeErr := reader.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return s, fmt.Errorf("%s: %w", target, err)
			}
			if previous.Hash != hex.EncodeToString(hash.Sum(nil)) {
				return s, fmt.Errorf("%s: conflicting library content", target)
			}
			s.Skipped++
		} else {
			if entry.UncompressedSize64 > uint64(^uint64(0)>>1)/2 {
				reader.Close()
				return s, fmt.Errorf("%s: entry too large", target)
			}
			release := func() {}
			if reserve != nil {
				// Stage and encrypted store coexist until commit; allow pack overhead.
				release, err = reserve(int64(entry.UncompressedSize64)*2 + 16<<20)
				if err != nil {
					reader.Close()
					return s, fmt.Errorf("%s: %w", target, err)
				}
			}
			mtime := entry.Modified
			if mtime.Before(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)) {
				mtime = time.Time{}
			}
			stored, putErr := lib.PutReaderAt(ctx, target, body, int64(entry.UncompressedSize64), mtime)
			err = putErr
			closeErr := reader.Close()
			release()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return s, fmt.Errorf("%s: %w", target, err)
			}
			existing[target] = stored
			s.Imported++
		}
		if albumMetadata {
			lib.RememberPhotoAlbumMetadata(target, existing[target].Hash, albumRaw.Bytes())
		}
		s.ProcessedBytes += entry.UncompressedSize64
		if progress != nil {
			progress(s)
		}
	}
	return s, nil
}

func ensureParents(ctx context.Context, lib *library.Library, existing map[string]catalog.File, target string) error {
	parent := path.Dir(target)
	if parent == "." {
		return nil
	}
	parts := strings.Split(parent, "/")
	for i := range parts {
		if err := ensureFolder(ctx, lib, existing, strings.Join(parts[:i+1], "/")); err != nil {
			return err
		}
	}
	return nil
}

func ensureFolder(ctx context.Context, lib *library.Library, existing map[string]catalog.File, name string) error {
	metadata, found := existing[name]
	if found {
		if !metadata.Folder {
			return fmt.Errorf("%s: conflicting library file", name)
		}
		return nil
	}
	if err := lib.Mkdir(ctx, name); err != nil {
		return err
	}
	existing[name] = catalog.File{Path: name, Folder: true, Present: true}
	return nil
}
