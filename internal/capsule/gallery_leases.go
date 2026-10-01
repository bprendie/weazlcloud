package capsule

import (
	"context"
	"io"
	"log"
	"path/filepath"
)

type galleryLease struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// Caller holds Store.mu. Leases protect frozen bytes while short admission
// transactions stay serialized; transfers themselves never hold the store lock.
func (s *Store) leaseGalleryLocked(id string) *galleryLease {
	ctx, cancel := context.WithCancel(context.Background())
	lease := &galleryLease{ctx: ctx, cancel: cancel}
	if s.galleryStreams == nil {
		s.galleryStreams = make(map[string]map[*galleryLease]struct{})
	}
	if s.galleryStreams[id] == nil {
		s.galleryStreams[id] = make(map[*galleryLease]struct{})
	}
	s.galleryStreams[id][lease] = struct{}{}
	return lease
}

func (s *Store) cancelGalleryLocked(id string) {
	for lease := range s.galleryStreams[id] {
		lease.cancel()
	}
}

func (s *Store) finishGallery(id string, lease *galleryLease) {
	lease.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.galleryStreams[id], lease)
	if len(s.galleryStreams[id]) != 0 {
		return
	}
	delete(s.galleryStreams, id)
	dir := filepath.Join(s.root, id)
	rec, err := readMeta(dir)
	if err == nil && !rec.Live() {
		if err := s.revokeDir(dir, rec); err != nil {
			log.Printf("gallery cleanup will retry id=%s: %v", id, err)
		}
	}
}

type galleryLeaseWriter struct {
	lease *galleryLease
	io.Writer
}

func (w galleryLeaseWriter) Write(body []byte) (int, error) {
	if err := w.lease.ctx.Err(); err != nil {
		return 0, err
	}
	return w.Writer.Write(body)
}
