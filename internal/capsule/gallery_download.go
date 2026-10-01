package capsule

import (
	"archive/zip"
	"fmt"
	"io"
	"path/filepath"
)

func (s *Store) GalleryDownload(id string, auth GalleryAuth, ids []string, open func(Record) (io.Writer, error)) (Record, error) {
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	rec, manifest, key, err := s.galleryManifestLocked(id, auth)
	if err != nil {
		return rec, err
	}
	defer clear(key)
	if open == nil || len(ids) > 10_000 {
		return rec, ErrGone
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validGalleryToken(id) || selected[id] {
			return rec, ErrGone
		}
		selected[id] = true
	}
	items := make([]GalleryItem, 0, len(ids))
	for _, item := range manifest.Items {
		if selected[item.ID] {
			items = append(items, item)
		}
	}
	if len(items) != len(ids) {
		return rec, ErrGone
	}
	dir := filepath.Join(s.root, id)
	rec.Used++
	rec.Revoked = rec.Used >= rec.Limit
	if err := writeMeta(dir, rec); err != nil {
		return rec, ErrStorage
	}
	lease := s.leaseGalleryLocked(id)
	s.mu.Unlock()
	locked = false
	defer s.finishGallery(id, lease)
	w, err := open(rec)
	if err != nil {
		return rec, err
	}
	w = galleryLeaseWriter{lease: lease, Writer: w}
	if len(ids) == 0 {
		return rec, readGalleryStream(filepath.Join(dir, "payload"), key, w)
	}
	z := zip.NewWriter(w)
	for index, item := range items {
		entry, err := z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%04d/%s", index+1, item.Name), Method: zip.Store})
		if err != nil {
			return rec, err
		}
		partKey := galleryKey(key, item.ID+".original")
		err = readGalleryStream(filepath.Join(dir, "gallery", item.ID+".original"), partKey, entry)
		clear(partKey)
		if err != nil {
			return rec, err
		}
	}
	return rec, z.Close()
}
