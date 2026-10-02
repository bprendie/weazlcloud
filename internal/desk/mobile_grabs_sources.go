package desk

import (
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/library"
)

func mobileGrabPrepare(r *http.Request, l *library.Library) capsule.MobilePrepare {
	return func(rec capsule.Record, _ capsule.StreamSource) (capsule.Record, capsule.StreamSource, func(), error) {
		// Existing sealFor validates the path and options. Capture all references at
		// once, then discard its path readers so replacement/move cannot change bytes.
		var body mintBody
		// canonicalMobileGrab leaves a reproducible body; handlers consume it, so use
		// the source's displayed path only for folders, and retain the requested path
		// separately through GetBody installed by the canonical decoder.
		if r.GetBody == nil {
			return rec, nil, nil, errors.New("missing immutable grab specification")
		}
		reader, err := r.GetBody()
		if err != nil {
			return rec, nil, nil, err
		}
		defer reader.Close()
		if err := decodeMobileMint(reader, &body); err != nil {
			return rec, nil, nil, err
		}
		manifest, err := l.PrepareArchive(r.Context(), []string{body.Path})
		if err != nil {
			return rec, nil, nil, err
		}
		fail := func(err error) (capsule.Record, capsule.StreamSource, func(), error) {
			manifest.Release()
			return rec, nil, nil, err
		}
		if manifest.Bytes != rec.Size || manifest.Files != len(rec.Files) {
			return fail(capsule.ErrMobileConflict)
		}
		rec.Files = nil
		rec.Size = manifest.Bytes
		var files []library.ArchiveEntry
		for _, entry := range manifest.Entries {
			if !entry.Folder {
				files = append(files, entry)
				rec.Files = append(rec.Files, capsule.Member{Title: path.Base(entry.Path), Size: entry.Size, Kind: "FILE"})
			}
		}
		if len(files) == 0 {
			return fail(capsule.ErrNeedPath)
		}
		if rec.Kind != "folder" {
			if len(files) != 1 {
				return fail(capsule.ErrNeedPath)
			}
			manifest.Entries = files
			return rec, func(dst io.Writer) error {
				out := &mobileZIPFileWriter{dst: dst, left: files[0].Size}
				_, _, err := l.WriteArchive(r.Context(), manifest, out)
				if err == nil && (!out.started || out.skip != 0 || out.left != 0) {
					err = io.ErrUnexpectedEOF
				}
				return err
			}, manifest.Release, nil
		}
		prefix := strings.TrimSuffix(body.Path, "/") + "/"
		selected := manifest.Entries[:0]
		for _, entry := range manifest.Entries {
			if !strings.HasPrefix(entry.Path, prefix) {
				continue
			}
			entry.Path = strings.TrimPrefix(entry.Path, prefix)
			selected = append(selected, entry)
		}
		manifest.Entries = selected
		return rec, func(dst io.Writer) error { _, _, err := l.WriteArchive(r.Context(), manifest, dst); return err }, manifest.Release, nil
	}
}

// WriteArchive uses zip.Store and streams a local header followed by exact file
// bytes. For a single-file grab, remove that envelope without plaintext staging.
// At most the fixed 30-byte header is buffered; central records are discarded.
type mobileZIPFileWriter struct {
	dst     io.Writer
	header  []byte
	skip    int
	left    int64
	started bool
}

func (w *mobileZIPFileWriter) Write(p []byte) (int, error) {
	total := len(p)
	if !w.started {
		n := min(30-len(w.header), len(p))
		w.header = append(w.header, p[:n]...)
		p = p[n:]
		if len(w.header) < 30 {
			return total, nil
		}
		if binary.LittleEndian.Uint32(w.header) != 0x04034b50 || binary.LittleEndian.Uint16(w.header[8:]) != 0 {
			return 0, errors.New("unexpected archive format")
		}
		w.skip = int(binary.LittleEndian.Uint16(w.header[26:])) + int(binary.LittleEndian.Uint16(w.header[28:]))
		w.started = true
	}
	n := min(w.skip, len(p))
	w.skip -= n
	p = p[n:]
	if w.skip == 0 && w.left > 0 {
		n := int(min(int64(len(p)), w.left))
		got, err := w.dst.Write(p[:n])
		w.left -= int64(got)
		if err != nil {
			return total - len(p) + got, err
		}
		if got != n {
			return total - len(p) + got, io.ErrShortWrite
		}
	}
	return total, nil
}
