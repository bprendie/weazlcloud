package takeout

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

// Each worker streams one entry to the existing durable library queue. Its
// result is accounted by the coordinator only after every worker has returned.
func importFile(ctx context.Context, lib *library.Library, entry *zip.File, target string, previous catalog.File, found bool, reserve Reserve, options []Options) (catalog.File, Summary, error) {
	var s Summary
	result := previous
	if err := ctx.Err(); err != nil {
		return result, s, err
	}
	reader, err := entry.Open()
	if err != nil {
		if skipCorrupt(options, &s, entry, err, nil) {
			return result, s, nil
		}
		return result, s, fmt.Errorf("%s: %w", target, err)
	}
	source := &entryReader{ReadCloser: reader}
	var albumRaw bytes.Buffer
	var body io.Reader = source
	albumMetadata := library.IsPhotoAlbumMetadata(target) && entry.UncompressedSize64 <= 1<<20
	if albumMetadata {
		body = io.TeeReader(source, &albumRaw)
	}
	if found {
		if previous.Folder || previous.Size < 0 || uint64(previous.Size) != entry.UncompressedSize64 {
			reader.Close()
			return result, s, fmt.Errorf("%s: conflicting library path", target)
		}
		hash := sha256.New()
		_, err = io.Copy(hash, body)
		closeErr := reader.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			if skipCorrupt(options, &s, entry, source.sourceError, nil) {
				return result, s, nil
			}
			return result, s, fmt.Errorf("%s: %w", target, err)
		}
		if previous.Hash != hex.EncodeToString(hash.Sum(nil)) {
			return result, s, fmt.Errorf("%s: conflicting library content", target)
		}
		s.Skipped++
	} else {
		if entry.UncompressedSize64 > uint64(^uint64(0)>>1)/2 {
			reader.Close()
			return result, s, fmt.Errorf("%s: entry too large", target)
		}
		release := func() {}
		if reserve != nil {
			// Stage and encrypted store coexist until commit; allow pack overhead.
			release, err = reserve(int64(entry.UncompressedSize64)*2 + 16<<20)
			if err != nil {
				reader.Close()
				return result, s, fmt.Errorf("%s: %w", target, err)
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
			if skipCorrupt(options, &s, entry, source.sourceError, nil) {
				return result, s, nil
			}
			return result, s, fmt.Errorf("%s: %w", target, err)
		}
		result = stored
		s.Imported++
	}
	if albumMetadata {
		lib.RememberPhotoAlbumMetadata(target, result.Hash, albumRaw.Bytes())
	}
	s.ProcessedBytes += entry.UncompressedSize64
	return result, s, nil
}
