package takeout

import (
	"archive/zip"
	"context"
	"fmt"
	"path"
	"sync"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

const importWorkers = 8
const parallelEntryLimit = 32 << 20

type importResult struct {
	file    catalog.File
	summary Summary
	err     error
}

// Small files fill the library's existing Restic batch queue. Waves have at
// most 256 MiB of expanded input; larger entries are streamed alone. Folder
// changes and repeated destinations are barriers, preserving path conflicts
// and deterministic duplicate handling without sharing mutable catalog maps.
func importEntries(ctx context.Context, lib *library.Library, z *zip.Reader, prefix string, existing map[string]catalog.File, s Summary, reserve Reserve, progress func(Summary), options []Options) (Summary, error) {
	var pending []*zip.File
	destinations := make(map[*zip.File]string)
	targets := make(map[string]bool)
	parent := ""
	directories := make(map[string]string)
	flush := func() error {
		results := make([]importResult, len(pending))
		var wg sync.WaitGroup
		for i, entry := range pending {
			target := destinations[entry]
			previous, found := existing[target]
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := &results[i]
				r.file, r.summary, r.err = importFile(ctx, lib, entry, target, previous, found, existing, reserve, options)
			}()
		}
		wg.Wait()
		var firstErr error
		for _, r := range results {
			if r.err != nil && firstErr == nil {
				firstErr = r.err
			}
			if r.summary.Imported+r.summary.Skipped > 0 {
				existing[r.file.Path] = r.file
			}
			s.Imported += r.summary.Imported
			s.Skipped += r.summary.Skipped
			s.ProcessedBytes += r.summary.ProcessedBytes
			s.Corrupt += r.summary.Corrupt
			s.CorruptBytes += r.summary.CorruptBytes
			s.Errors = append(s.Errors, r.summary.Errors...)
			s.Renamed = append(s.Renamed, r.summary.Renamed...)
			if progress != nil {
				progress(s)
			}
		}
		pending = nil
		clear(targets)
		clear(destinations)
		return firstErr
	}
	for _, entry := range z.File {
		target, _ := Destination(prefix, entry.Name) // Scan validated all paths.
		if target == "" && entry.FileInfo().IsDir() {
			continue
		}
		large := entry.UncompressedSize64 > parallelEntryLimit
		folder := entry.FileInfo().IsDir()
		_, exists := existing[target]
		if len(pending) > 0 && (len(pending) == importWorkers || targets[target] || parent != path.Dir(target) || folder || large || exists || ctx.Err() != nil) {
			if err := flush(); err != nil {
				return s, err
			}
		}
		if err := ctx.Err(); err != nil {
			return s, err
		}
		resolved, renamed, err := resolveFolders(ctx, lib, existing, target, folder)
		if err != nil {
			return s, fmt.Errorf("%s: %w", target, err)
		}
		for _, r := range renamed {
			if directories[r.Original] != r.Path {
				directories[r.Original] = r.Path
				s.Directories = append(s.Directories, r)
			}
		}
		if folder {
			continue
		}
		_, resolvedExists := existing[resolved]
		if len(pending) > 0 && (targets[resolved] || resolvedExists) {
			if err := flush(); err != nil {
				return s, err
			}
		}
		pending = append(pending, entry)
		destinations[entry] = resolved
		targets[target] = true
		targets[resolved] = true
		parent = path.Dir(target)
		// Conflicts can allocate alternate paths. Process existing destinations
		// alone so no peer can race that alternate during a wave.
		_, exists = existing[resolved]
		if large || exists {
			if err := flush(); err != nil {
				return s, err
			}
		}
	}
	err := flush()
	return s, err
}
