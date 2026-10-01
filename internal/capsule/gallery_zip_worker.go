package capsule

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (s *Store) startGalleryZIPLocked(id string, key []byte, job galleryZIPJob) {
	activeID := id + ":" + job.ID
	if s.galleryZIPActive == nil {
		s.galleryZIPActive = make(map[string]bool)
	}
	if s.galleryZIPActive[activeID] {
		return
	}
	if len(s.galleryZIPActive) >= 64 {
		return
	}
	if s.galleryZIPSlots == nil {
		s.galleryZIPSlots = make(chan struct{}, 2)
	}
	s.galleryZIPActive[activeID] = true
	lease := s.leaseGalleryLocked(id)
	key = append([]byte(nil), key...)
	reserve := s.galleryReserve
	go s.runGalleryZIP(id, key, job, lease, reserve)
}

func (s *Store) runGalleryZIP(id string, key []byte, job galleryZIPJob, lease *galleryLease, reserve func(int64) (func(), error)) {
	defer clear(key)
	defer s.finishGallery(id, lease)
	defer func() { s.mu.Lock(); delete(s.galleryZIPActive, id+":"+job.ID); s.mu.Unlock() }()
	select {
	case s.galleryZIPSlots <- struct{}{}:
		defer func() { <-s.galleryZIPSlots }()
	case <-lease.ctx.Done():
		return
	}
	path := filepath.Join(s.root, id, "gallery", "zip", job.ID+".wza")
	// A restarted preparing job rebuilds its frozen selection; it never needs
	// the owner's vault or original pathname to do so.
	_ = os.Remove(path)
	if leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".zip-"+job.ID+"-*")); err == nil {
		for _, name := range leftovers {
			_ = os.Remove(name)
		}
	}
	bytes := int64(4096)
	for _, item := range job.Items {
		if item.Size < 0 || item.Size > math.MaxInt64-bytes-4096 {
			s.failGalleryZIP(id, key, job, "archive size overflow")
			return
		}
		bytes += item.Size + 4096
	}
	frames := bytes/(1<<20) + 1
	if bytes > math.MaxInt64-frames*28 {
		s.failGalleryZIP(id, key, job, "archive size overflow")
		return
	}
	release := func() {}
	if reserve != nil {
		var err error
		release, err = reserve(bytes + frames*28)
		if err != nil {
			s.failGalleryZIP(id, key, job, "not enough space to prepare ZIP")
			return
		}
	}
	defer release()
	job.Status = "preparing"
	if err := s.checkpointGalleryZIP(id, key, job, lease); err != nil {
		log.Printf("gallery ZIP checkpoint failed: %v", err)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".zip-"+job.ID+"-*")
	if err != nil {
		s.failGalleryZIP(id, key, job, "archive staging unavailable")
		return
	}
	defer os.Remove(tmp.Name())
	_ = tmp.Chmod(0600)
	zipKey := galleryKey(key, "zip:"+job.ID+":bytes")
	defer clear(zipKey)
	out, err := cryptox.NewStreamFileWriter(tmp, zipKey)
	if err == nil {
		z := zip.NewWriter(galleryLeaseWriter{lease: lease, Writer: out})
		for n, item := range job.Items {
			var entry io.Writer
			entry, err = z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%04d/%s", n+1, item.Name), Method: zip.Store})
			if err != nil {
				break
			}
			partKey := galleryKey(key, item.ID+".original")
			err = readGalleryStream(filepath.Join(s.root, id, "gallery", item.ID+".original"), partKey, entry)
			clear(partKey)
			if err != nil {
				break
			}
		}
		if err == nil {
			err = z.Close()
		}
		closeErr := out.Close()
		if err == nil {
			err = closeErr
		}
		job.Bytes = out.Size()
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = lease.ctx.Err()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		s.failGalleryZIP(id, key, job, "archive preparation failed")
		return
	}
	job.Status = "ready"
	if err := s.checkpointGalleryZIP(id, key, job, lease); err != nil {
		_ = os.Remove(path)
		log.Printf("gallery ZIP ready checkpoint failed: %v", err)
	}
}

func (s *Store) failGalleryZIP(id string, key []byte, job galleryZIPJob, message string) {
	job.Status = "failed"
	job.Error = message
	// Revocation may already have removed the directory; do not recreate it.
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, err := readMeta(filepath.Join(s.root, id))
	if err == nil && rec.Live() {
		if err := s.writeGalleryZIP(id, key, job); err != nil {
			log.Printf("gallery ZIP failure checkpoint: %v", err)
		}
	}
}

func (s *Store) DownloadGalleryZIP(id string, auth GalleryAuth, jobID string, open func(Record) (io.Writer, error)) (Record, error) {
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	rec, _, key, err := s.galleryManifestLocked(id, auth)
	if err != nil {
		return rec, err
	}
	defer clear(key)
	if !validGalleryToken(jobID) || open == nil {
		return rec, ErrGone
	}
	job, err := s.readGalleryZIP(id, jobID, key)
	if err != nil || job.Status != "ready" {
		return rec, ErrGone
	}
	lease := s.leaseGalleryLocked(id)
	zipKey := galleryKey(key, "zip:"+job.ID+":bytes")
	defer clear(zipKey)
	reader, err := cryptox.OpenStreamFile(lease.ctx, filepath.Join(s.root, id, "gallery", "zip", job.ID+".wza"), zipKey, job.Bytes)
	if err != nil {
		delete(s.galleryStreams[id], lease)
		lease.cancel()
		return rec, ErrGone
	}
	defer reader.Close()
	rec.Used++
	rec.Revoked = rec.Used >= rec.Limit
	if err := writeMeta(filepath.Join(s.root, id), rec); err != nil {
		delete(s.galleryStreams[id], lease)
		lease.cancel()
		return rec, ErrStorage
	}
	s.mu.Unlock()
	locked = false
	defer s.finishGallery(id, lease)
	dst, err := open(rec)
	if err != nil {
		return rec, err
	}
	_, err = io.CopyBuffer(galleryLeaseWriter{lease: lease, Writer: dst}, reader, make([]byte, 256<<10))
	return rec, err
}

func (s *Store) checkpointGalleryZIP(id string, key []byte, job galleryZIPJob, lease *galleryLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lease.ctx.Err() != nil {
		return ErrGone
	}
	rec, err := readMeta(filepath.Join(s.root, id))
	if err != nil || !rec.Live() {
		return ErrGone
	}
	return s.writeGalleryZIP(id, key, job)
}
