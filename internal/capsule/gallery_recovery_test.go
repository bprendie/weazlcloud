package capsule

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGalleryTransfersReleaseStoreLockAndRetainEarlierAdmissions(t *testing.T) {
	s := New(t.TempDir())
	rec := galleryFixture(t, s, "open", 2)
	manifest, token, err := s.GallerySession(rec.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := s.GalleryOriginal(rec.ID, GalleryAuth{Session: token}, manifest.Items[0].ID, func(Record, GalleryItem) (io.Writer, error) { close(entered); <-resume; return io.Discard, nil })
		done <- err
	}()
	<-entered
	checked := make(chan error, 1)
	go func() { _, err := s.Meta(rec.ID); checked <- err }()
	select {
	case err := <-checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow guest blocked metadata")
	}
	if _, err := s.GalleryOriginal(rec.ID, GalleryAuth{Session: token}, manifest.Items[1].ID, func(Record, GalleryItem) (io.Writer, error) { return io.Discard, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Meta(rec.ID); !errors.Is(err, ErrGone) {
		t.Fatal("final admission did not close grant")
	}
	if _, err := os.Stat(filepath.Join(s.root, rec.ID, "gallery")); err != nil {
		t.Fatal("burn removed earlier admitted bytes")
	}
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.root, rec.ID, "gallery")); !os.IsNotExist(err) {
		t.Fatal("burn retained gallery")
	}
}

func TestGalleryExplicitRevocationCancelsAdmittedTransfer(t *testing.T) {
	s := New(t.TempDir())
	rec := galleryFixture(t, s, "open", 3)
	manifest, token, err := s.GallerySession(rec.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := s.GalleryOriginal(rec.ID, GalleryAuth{Session: token}, manifest.Items[0].ID, func(Record, GalleryItem) (io.Writer, error) { close(entered); <-resume; return io.Discard, nil })
		done <- err
	}()
	<-entered
	if err := s.RevokeOwner(rec.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-done; err == nil {
		t.Fatal("revocation allowed transfer to proceed")
	}
}

func TestGalleryZIPRecoversQueuedJobWithoutSpendingRetry(t *testing.T) {
	s := New(t.TempDir())
	rec := galleryFixture(t, s, "open", 3)
	manifest, token, err := s.GallerySession(rec.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	auth := GalleryAuth{Session: token}
	s.galleryZIPSlots = make(chan struct{}, 2)
	s.galleryZIPSlots <- struct{}{}
	s.galleryZIPSlots <- struct{}{}
	job, err := s.PrepareGalleryZIP(rec.ID, auth, []string{manifest.Items[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.cancelGalleryLocked(rec.ID)
	s.mu.Unlock()
	for deadline := time.Now().Add(time.Second); ; {
		s.mu.Lock()
		active := len(s.galleryZIPActive)
		s.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not stop")
		}
		time.Sleep(time.Millisecond)
	}
	restarted := New(s.root)
	for deadline := time.Now().Add(3 * time.Second); ; {
		job, err = restarted.GalleryZIPStatus(rec.ID, auth, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "ready" {
			break
		}
		if job.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("job=%+v", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
	meta, err := restarted.Meta(rec.ID)
	if err != nil || meta.Used != 0 {
		t.Fatal("ZIP preparation spent a retry")
	}
	for _, extension := range []string{".enc", ".wza"} {
		raw, err := os.ReadFile(filepath.Join(s.root, rec.ID, "gallery", "zip", job.ID+extension))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("private-video")) || bytes.Contains(raw, []byte("original:")) {
			t.Fatal("plaintext ZIP/checkpoint")
		}
	}
	final := New(s.root)
	if err := os.Remove(filepath.Join(s.root, rec.ID, "gallery", "zip", job.ID+".wza")); err != nil {
		t.Fatal(err)
	}
	job, err = final.GalleryZIPStatus(rec.ID, auth, job.ID)
	if err != nil || job.Status != "failed" {
		t.Fatalf("missing ZIP advertised as ready: %+v %v", job, err)
	}
	job, err = final.PrepareGalleryZIP(rec.ID, auth, []string{manifest.Items[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		job, err = final.GalleryZIPStatus(rec.ID, auth, job.ID)
		if err != nil || job.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("missing-output recovery: %+v %v", job, err)
		}
		if job.Status == "ready" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	meta, err = final.Meta(rec.ID)
	if err != nil || meta.Used != 0 {
		t.Fatal("rebuilding missing ZIP spent a retry")
	}
	var body bytes.Buffer
	if _, err := final.DownloadGalleryZIP(rec.ID, auth, job.ID, func(Record) (io.Writer, error) { return &body, nil }); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(body.Bytes()), int64(body.Len()))
	if err != nil || len(z.File) != 1 {
		t.Fatalf("ZIP=%v", err)
	}
	f, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(raw) != "original:private-video.mp4" {
		t.Fatalf("readback=%q %v", raw, err)
	}
	if _, renewed, err := final.RenewGallerySession(rec.ID, auth); err != nil || renewed == "" {
		t.Fatalf("session renewal=%v", err)
	}
	if err := final.DeleteOwner("alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.root, rec.ID)); !os.IsNotExist(err) {
		t.Fatal("account deletion retained ZIP")
	}
}
