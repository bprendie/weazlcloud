package capsule

import (
	"bytes"
	"io"
	"testing"
	"time"
)

func TestLiveGalleryFrozenPairAndMotionDoNotSpendDownload(t *testing.T) {
	s := New(t.TempDir())
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			active := len(s.galleryZIPActive)
			s.mu.Unlock()
			if active == 0 {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Error("gallery ZIP worker did not finish")
	})
	sources := []GallerySource{
		{SourceID: "owner-still-id", Item: GalleryItem{Name: "still.jpg", MediaType: "image/jpeg", Size: 5, Revision: 1}, Original: func(w io.Writer) error { _, err := w.Write([]byte("still")); return err }},
		{SourceID: "owner-motion-id", ParentID: "owner-still-id", Item: GalleryItem{Name: "motion.mov", MediaType: "video/quicktime", Size: 6, Revision: 1}, Original: func(w io.Writer) error { _, err := w.Write([]byte("motion")); return err }, MotionPreview: func() ([]byte, error) { return []byte("safe-compatible-motion"), nil }},
	}
	rec, err := s.MintGallery(Record{Owner: "alice", Name: "Photos.zip", Label: "Photos", Gate: "open", Limit: 3, Expires: time.Now().Add(time.Hour)}, "", sources)
	if err != nil {
		t.Fatal(err)
	}
	manifest, session, err := s.GallerySession(rec.ID, "")
	if err != nil || len(manifest.Items) != 2 {
		t.Fatal(manifest, err)
	}
	still, motion := manifest.Items[0], manifest.Items[1]
	if motion.ParentID != still.ID || still.MotionID != motion.ID || still.ID == "owner-still-id" || motion.ID == "owner-motion-id" {
		t.Fatal("private IDs or pair lost", manifest)
	}
	var buffer bytes.Buffer
	if _, err := s.GalleryMotion(rec.ID, GalleryAuth{Session: session}, motion.ID, &buffer); err != nil || buffer.String() != "safe-compatible-motion" {
		t.Fatal(buffer.String(), err)
	}
	after, err := s.Meta(rec.ID)
	if err != nil || after.Used != rec.Used {
		t.Fatal("motion spent grab", after, err)
	}
	job, err := s.PrepareGalleryZIP(rec.ID, GalleryAuth{Session: session}, []string{still.ID})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "queued" && job.Status != "preparing" && job.Status != "ready" {
		t.Fatal(job)
	}
	_, _, key, err := func() (Record, GalleryManifest, []byte, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.galleryManifestLocked(rec.ID, GalleryAuth{Session: session})
	}()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	stored, err := s.readGalleryZIP(rec.ID, job.ID, key)
	if err != nil || len(stored.Items) != 2 {
		t.Fatal("selected pair ZIP lost component", stored, err)
	}
}
