package takeout

import (
	"archive/zip"
	"compress/flate"
	"errors"
	"io"
)

type Options struct{ SkipCorrupt bool }
type EntryFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
	Bytes uint64 `json:"bytes"`
}

// Track source errors separately so quota, vault and backend write failures
// cannot be mistaken for corrupt input and silently skipped.
type entryReader struct {
	io.ReadCloser
	sourceError error
}

func (r *entryReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		r.sourceError = err
	}
	return n, err
}

func skipCorrupt(options []Options, summary *Summary, entry *zip.File, err error, progress func(Summary)) bool {
	if len(options) == 0 || !options[0].SkipCorrupt || err == nil {
		return false
	}
	var deflateError flate.CorruptInputError
	if !errors.Is(err, zip.ErrChecksum) && !errors.Is(err, zip.ErrFormat) && !errors.Is(err, zip.ErrAlgorithm) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.As(err, &deflateError) {
		return false
	}
	summary.Corrupt++
	summary.CorruptBytes += entry.UncompressedSize64
	summary.ProcessedBytes += entry.UncompressedSize64
	summary.Errors = append(summary.Errors, EntryFailure{Path: entry.Name, Error: err.Error(), Bytes: entry.UncompressedSize64})
	if progress != nil {
		progress(*summary)
	}
	return true
}
