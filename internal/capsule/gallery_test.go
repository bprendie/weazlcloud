package capsule

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func galleryFixture(t *testing.T, s *Store, gate string, limit int) Record {
	t.Helper()
	sources := []GallerySource{}
	for _, name := range []string{"secret-photo.jpg", "private-video.mp4"} {
		data := []byte("original:" + name)
		sources = append(sources, GallerySource{Item: GalleryItem{Name: name, MediaType: "image/jpeg", Size: int64(len(data)), Revision: 7}, Original: func(w io.Writer) error { _, err := w.Write(data); return err }, Preview: func() ([]byte, string, error) { return []byte("sanitized-pixels"), "image/jpeg", nil }})
	}
	rec, err := s.MintGallery(Record{Owner: "alice", Name: "Shared photos.zip", Label: "Trip", Gate: gate, Limit: limit, Expires: time.Now().Add(time.Hour)}, "photo-secret", sources)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestGalleryFrozenEncryptedManifestAndNonConsumingPreviews(t *testing.T) {
	s := New(t.TempDir())
	rec := galleryFixture(t, s, "passphrase", 3)
	if _, err := s.GalleryInfo(rec.ID, "wrong"); !errors.Is(err, ErrPhrase) {
		t.Fatalf("wrong phrase=%v", err)
	}
	manifest, token, err := s.GallerySession(rec.ID, "photo-secret")
	if err != nil || len(manifest.Items) != 2 || token == "" {
		t.Fatalf("manifest=%+v error=%v", manifest, err)
	}
	auth := GalleryAuth{Session: token}
	for i := 0; i < 3; i++ {
		var out bytes.Buffer
		item, err := s.GalleryPreview(rec.ID, auth, manifest.Items[0].ID, &out)
		if err != nil || out.String() != "sanitized-pixels" || item.Revision != 7 {
			t.Fatalf("preview=%+v error=%v", item, err)
		}
	}
	meta, err := s.Meta(rec.ID)
	if err != nil || meta.Used != 0 {
		t.Fatalf("preview consumed retry: %+v error=%v", meta, err)
	}
	if _, err := s.GalleryOriginal(rec.ID, auth, "../../payload", func(Record, GalleryItem) (io.Writer, error) { return io.Discard, nil }); !errors.Is(err, ErrGone) {
		t.Fatalf("substitution=%v", err)
	}
	other := galleryFixture(t, s, "open", 2)
	if _, err := s.GalleryPreview(other.ID, auth, manifest.Items[0].ID, io.Discard); !errors.Is(err, ErrPhrase) {
		t.Fatalf("cross grant token=%v", err)
	}
	err = filepath.WalkDir(filepath.Join(s.root, rec.ID), func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		for _, secret := range []string{"secret-photo.jpg", "private-video.mp4", "sanitized-pixels", "photo-secret"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatalf("plaintext %q in %s", secret, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err := s.GalleryDownload(rec.ID, auth, []string{manifest.Items[1].ID}, func(Record) (io.Writer, error) { return &archive, nil }); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil || len(z.File) != 1 {
		t.Fatalf("selected ZIP error=%v", err)
	}
	f, err := z.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(payload) != "original:private-video.mp4" {
		t.Fatalf("ZIP payload=%q error=%v", payload, err)
	}
	if err := s.RevokeOwner(rec.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GalleryPreview(rec.ID, auth, manifest.Items[0].ID, io.Discard); !errors.Is(err, ErrGone) {
		t.Fatalf("revoked token=%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.root, rec.ID, "gallery")); !os.IsNotExist(err) {
		t.Fatalf("gallery assets retained after revoke: %v", err)
	}
}

func TestGalleryConcurrentFinalTransferBurnsAndCleansAssets(t *testing.T) {
	s := New(t.TempDir())
	rec := galleryFixture(t, s, "open", 2)
	manifest, token, err := s.GallerySession(rec.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.GalleryOriginal(rec.ID, GalleryAuth{Session: token}, manifest.Items[0].ID, func(Record, GalleryItem) (io.Writer, error) { return io.Discard, nil })
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrGone) {
			t.Fatal(err)
		}
	}
	if success != 2 {
		t.Fatalf("admitted %d transfers", success)
	}
	for _, part := range []string{"gallery", "payload", "open.key"} {
		if _, err := os.Stat(filepath.Join(s.root, rec.ID, part)); !os.IsNotExist(err) {
			t.Fatalf("burn retained %s: %v", part, err)
		}
	}
}

func TestGalleryMintFailureDoesNotPublishPartialSelection(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.MintGallery(Record{Limit: 1, Expires: time.Now().Add(time.Hour)}, "", []GallerySource{{Item: GalleryItem{Name: "a.jpg", Size: 100}, Original: func(w io.Writer) error { _, err := io.Copy(w, strings.NewReader("short")); return err }}})
	if err == nil {
		t.Fatal("mint accepted changed size")
	}
	entries, _ := os.ReadDir(s.root)
	if len(entries) != 0 {
		t.Fatalf("partial mint left %d entries", len(entries))
	}
}
