package capsule

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

func validGalleryToken(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func (s *Store) galleryManifestLocked(id string, auth GalleryAuth) (Record, GalleryManifest, []byte, error) {
	if !validGalleryToken(id) {
		return Record{}, GalleryManifest{}, nil, ErrGone
	}
	dir := filepath.Join(s.root, id)
	rec, err := readMeta(dir)
	if err != nil || rec.Kind != "gallery" || !rec.Live() {
		return rec, GalleryManifest{}, nil, ErrGone
	}
	key, err := s.galleryAuthKeyLocked(dir, rec, auth)
	if err != nil {
		return rec, GalleryManifest{}, nil, err
	}
	if manifest, ok := s.galleryCache[id]; ok {
		return rec, manifest, key, nil
	}
	manifestKey := galleryKey(key, "manifest")
	defer clear(manifestKey)
	var raw bytes.Buffer
	err = readGalleryStream(filepath.Join(dir, "gallery", "manifest"), manifestKey, &galleryLimitWriter{Writer: &raw, Remaining: 16 << 20})
	defer clear(raw.Bytes())
	var manifest GalleryManifest
	if err != nil || json.Unmarshal(raw.Bytes(), &manifest) != nil {
		clear(key)
		return rec, manifest, nil, ErrGone
	}
	if s.galleryCache == nil {
		s.galleryCache = make(map[string]GalleryManifest)
	}
	if len(s.galleryCacheOrder) >= 16 {
		delete(s.galleryCache, s.galleryCacheOrder[0])
		s.galleryCacheOrder = s.galleryCacheOrder[1:]
	}
	s.galleryCacheOrder = append(s.galleryCacheOrder, id)
	s.galleryCache[id] = manifest
	return rec, manifest, key, nil
}

type galleryLimitWriter struct {
	io.Writer
	Remaining int
}

func (w *galleryLimitWriter) Write(p []byte) (int, error) {
	if len(p) > w.Remaining {
		return 0, ErrGone
	}
	w.Remaining -= len(p)
	return w.Writer.Write(p)
}

// GalleryInfo and previews never spend a retry. Passphrases are supplied in
// request bodies, not URLs or manifest download links.
func (s *Store) GalleryInfo(id, phrase string) (GalleryManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, manifest, key, err := s.galleryManifestLocked(id, GalleryAuth{Passphrase: phrase})
	clear(key)
	return manifest, err
}

func (s *Store) GalleryPreview(id string, auth GalleryAuth, itemID string, dst io.Writer) (GalleryItem, error) {
	return s.galleryDerivative(id, auth, itemID, dst, false)
}
func (s *Store) GalleryMotion(id string, auth GalleryAuth, itemID string, dst io.Writer) (GalleryItem, error) {
	return s.galleryDerivative(id, auth, itemID, dst, true)
}
func (s *Store) galleryDerivative(id string, auth GalleryAuth, itemID string, dst io.Writer, motion bool) (GalleryItem, error) {
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	_, manifest, key, err := s.galleryManifestLocked(id, auth)
	if err != nil {
		return GalleryItem{}, err
	}
	defer clear(key)
	for _, item := range manifest.Items {
		if item.ID != itemID {
			continue
		}
		kind := ".preview"
		if motion {
			kind = ".motion"
			item.PreviewType = item.MotionType
		}
		if item.PreviewType == "" {
			return GalleryItem{}, ErrGone
		}
		lease := s.leaseGalleryLocked(id)
		s.mu.Unlock()
		locked = false
		defer s.finishGallery(id, lease)
		partKey := galleryKey(key, item.ID+kind)
		defer clear(partKey)
		return item, readGalleryStream(filepath.Join(s.root, id, "gallery", item.ID+kind), partKey, &galleryLimitWriter{Writer: galleryLeaseWriter{lease: lease, Writer: dst}, Remaining: 8 << 20})
	}
	return GalleryItem{}, ErrGone
}

// Original transfer admission consumes one retry even when the recipient
// interrupts it. The final admitted transfer burns the gallery after streaming.
func (s *Store) GalleryOriginal(id string, auth GalleryAuth, itemID string, open func(Record, GalleryItem) (io.Writer, error)) (Record, error) {
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
	if open == nil {
		return rec, ErrGone
	}
	var item GalleryItem
	for _, candidate := range manifest.Items {
		if candidate.ID == itemID {
			item = candidate
			break
		}
	}
	if item.ID == "" {
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
	w, err := open(rec, item)
	if err != nil {
		return rec, err
	}
	partKey := galleryKey(key, item.ID+".original")
	defer clear(partKey)
	err = readGalleryStream(filepath.Join(dir, "gallery", item.ID+".original"), partKey, galleryLeaseWriter{lease: lease, Writer: w})
	return rec, err
}

func cleanupGallery(dir string) error { return os.RemoveAll(filepath.Join(dir, "gallery")) }
