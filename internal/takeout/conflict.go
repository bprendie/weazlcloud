package takeout

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

var errImportConflict = errors.New("conflicting library content or path")

// EntryRename is an auditable mapping, including enough ZIP identity to
// distinguish members whose source spellings normalize to the same path.
type EntryRename struct {
	Source   string `json:"source"`
	CRC32    uint32 `json:"crc32"`
	Size     uint64 `json:"size"`
	Original string `json:"original"`
	Path     string `json:"path"`
	Hash     string `json:"sha256"`
	Variant  int    `json:"variant,omitempty"`
}

func conflictPath(target string, entry *zip.File, variant int) string {
	identity := fmt.Sprintf("%s\x00%s\x00%d\x00%d", target, entry.Name, entry.CRC32, entry.UncompressedSize64)
	digest := sha256.Sum256([]byte(identity))
	ext := path.Ext(target)
	suffix := fmt.Sprintf("takeout-%x", digest[:16])
	if variant > 0 {
		suffix += fmt.Sprintf("-%d", variant+1)
	}
	return strings.TrimSuffix(target, ext) + " (" + suffix + ")" + ext
}

// Never replace the existing version. An alternate name is deterministic on
// retry, and its actual SHA-256 is verified before reusing it. A conflicting
// alternate advances to another stable candidate without overwriting it.
func importFile(ctx context.Context, lib *library.Library, entry *zip.File, target string, previous catalog.File, found bool, existing map[string]catalog.File, reserve Reserve, options []Options) (catalog.File, Summary, error) {
	file, s, err := importFileAt(ctx, lib, entry, target, previous, found, reserve, options)
	if !errors.Is(err, errImportConflict) {
		return file, s, err
	}
	for variant := 0; ; variant++ {
		alternate := conflictPath(target, entry, variant)
		prior, exists := existing[alternate]
		file, s, err = importFileAt(ctx, lib, entry, alternate, prior, exists, reserve, options)
		if errors.Is(err, errImportConflict) {
			continue
		}
		if err == nil && s.Imported+s.Skipped > 0 {
			s.Renamed = append(s.Renamed, EntryRename{Source: entry.Name, CRC32: entry.CRC32, Size: entry.UncompressedSize64, Original: target, Path: alternate, Hash: file.Hash, Variant: variant})
		}
		return file, s, err
	}
}
