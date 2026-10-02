package capsule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type GalleryZIPView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error,omitempty"`
}
type galleryZIPJob struct {
	Version int `json:"version"`
	GalleryZIPView
	Items []GalleryItem `json:"items"`
}

// The shared node quota covers additional frozen selection ZIPs. Preparation
// spends no grab; the completed ZIP remains until the capsule is burnt.
func (s *Store) SetGalleryReservation(reserve func(int64) (func(), error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.galleryReserve = reserve
}

func (s *Store) PrepareGalleryZIP(id string, auth GalleryAuth, ids []string) (GalleryZIPView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, manifest, key, err := s.galleryManifestLocked(id, auth)
	if err != nil {
		return GalleryZIPView{}, err
	}
	defer clear(key)
	if len(ids) == 0 || len(ids) > 10000 {
		return GalleryZIPView{}, ErrGone
	}
	selected := make(map[string]bool, len(ids))
	ordered := append([]string(nil), ids...)
	for _, itemID := range ids {
		if !validGalleryToken(itemID) || selected[itemID] {
			return GalleryZIPView{}, ErrGone
		}
		selected[itemID] = true
	}
	for _, item := range manifest.Items {
		if item.ParentID != "" && selected[item.ParentID] && !selected[item.ID] {
			selected[item.ID] = true
			ordered = append(ordered, item.ID)
		}
	}
	sort.Strings(ordered)
	raw, _ := json.Marshal(ordered)
	sum := sha256.Sum256(raw)
	jobID := hex.EncodeToString(sum[:16])
	job, err := s.readGalleryZIP(id, jobID, key)
	if err == nil {
		if err := s.validateGalleryZIPReady(id, key, &job); err != nil {
			return GalleryZIPView{}, err
		}
		if job.Status == "failed" {
			job.Status, job.Error = "queued", ""
			if err := s.writeGalleryZIP(id, key, job); err != nil {
				return GalleryZIPView{}, err
			}
		}
		if job.Status == "queued" || job.Status == "preparing" {
			s.startGalleryZIPLocked(id, key, job)
		}
		return job.GalleryZIPView, nil
	}
	if !os.IsNotExist(err) {
		return GalleryZIPView{}, ErrStorage
	}
	job = galleryZIPJob{Version: 1, GalleryZIPView: GalleryZIPView{ID: jobID, Status: "queued"}, Items: []GalleryItem{}}
	for _, item := range manifest.Items {
		if selected[item.ID] {
			job.Items = append(job.Items, item)
		}
	}
	if len(job.Items) != len(selected) {
		return GalleryZIPView{}, ErrGone
	}
	dir := filepath.Join(s.root, id, "gallery", "zip")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return GalleryZIPView{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return GalleryZIPView{}, err
	}
	count := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".enc" {
			count++
		}
	}
	if count >= 32 {
		return GalleryZIPView{}, ErrStorage
	}
	if err := s.writeGalleryZIP(id, key, job); err != nil {
		return GalleryZIPView{}, err
	}
	s.startGalleryZIPLocked(id, key, job)
	return job.GalleryZIPView, nil
}

func (s *Store) GalleryZIPStatus(id string, auth GalleryAuth, jobID string) (GalleryZIPView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, key, err := s.galleryManifestLocked(id, auth)
	if err != nil {
		return GalleryZIPView{}, err
	}
	defer clear(key)
	if !validGalleryToken(jobID) {
		return GalleryZIPView{}, ErrGone
	}
	job, err := s.readGalleryZIP(id, jobID, key)
	if err != nil {
		return GalleryZIPView{}, ErrGone
	}
	if err := s.validateGalleryZIPReady(id, key, &job); err != nil {
		return GalleryZIPView{}, err
	}
	if job.Status == "queued" || job.Status == "preparing" {
		s.startGalleryZIPLocked(id, key, job)
	}
	return job.GalleryZIPView, nil
}

// Metadata alone must not advertise a missing/truncated output as downloadable.
// Preparation can rebuild it from frozen sources without spending a grab.
func (s *Store) validateGalleryZIPReady(id string, key []byte, job *galleryZIPJob) error {
	if job.Status != "ready" {
		return nil
	}
	zipKey := galleryKey(key, "zip:"+job.ID+":bytes")
	defer clear(zipKey)
	reader, err := cryptox.OpenStreamFile(context.Background(), filepath.Join(s.root, id, "gallery", "zip", job.ID+".wza"), zipKey, job.Bytes)
	if err == nil {
		return reader.Close()
	}
	job.Status, job.Error, job.Bytes = "failed", "prepared ZIP unavailable; prepare again", 0
	return s.writeGalleryZIP(id, key, *job)
}

func (s *Store) writeGalleryZIP(id string, key []byte, job galleryZIPJob) error {
	plain, err := json.Marshal(job)
	if err != nil {
		return err
	}
	defer clear(plain)
	metaKey := galleryKey(key, "zip:"+job.ID+":meta")
	defer clear(metaKey)
	nonce, body, err := cryptox.Seal(metaKey, plain)
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(filepath.Join(s.root, id, "gallery", "zip", job.ID+".enc"), append(nonce, body...), 0600)
}

func (s *Store) readGalleryZIP(id, jobID string, key []byte) (galleryZIPJob, error) {
	var job galleryZIPJob
	f, err := os.Open(filepath.Join(s.root, id, "gallery", "zip", jobID+".enc"))
	if err != nil {
		return job, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil || len(body) < 12 || len(body) >= 8<<20 {
		return job, ErrStorage
	}
	metaKey := galleryKey(key, "zip:"+jobID+":meta")
	defer clear(metaKey)
	plain, err := cryptox.Open(metaKey, body[:12], body[12:])
	if err != nil {
		return job, err
	}
	defer clear(plain)
	if json.Unmarshal(plain, &job) != nil || job.Version != 1 || job.ID != jobID || job.Bytes < 0 || len(job.Items) == 0 || len(job.Items) > 10000 {
		return job, ErrStorage
	}
	return job, nil
}
