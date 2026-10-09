package library

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitPreparation(t *testing.T, l *Library, want string) photoPreparation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := l.PhotoPreparation()
		if err != nil {
			t.Fatal(err)
		}
		l.photoPrepMu.Lock()
		running := l.photoPrepRunning
		l.photoPrepMu.Unlock()
		if s.Status == want && !running {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("wanted %s, got %+v running=%v", want, s, running)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPhotoManualPauseSurvivesIndexChangesAndRestart(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	putPreviewFixture(t, l, "Photos/a.png")
	if _, err := l.SetPhotoPreparation(context.Background(), "pause"); err != nil {
		t.Fatal(err)
	}
	putPreviewFixture(t, l, "Photos/b.png")
	if _, err := l.PhotoPage(context.Background(), 10, "", ""); err != nil {
		t.Fatal(err)
	}
	if s, _ := l.PhotoPreparation(); !s.Paused || s.Status != "paused" {
		t.Fatalf("pause lost: %+v", s)
	}
	l.photoPrepMu.Lock()
	l.photoPrepLoaded = false
	l.photoPrep = photoPreparation{}
	l.photoPrepMu.Unlock()
	l.ResumePhotoPreparation(context.Background())
	if s, _ := l.PhotoPreparation(); !s.Paused || s.Status != "paused" {
		t.Fatalf("restart lost pause: %+v", s)
	}
}

func TestPhotoRestartDiscardsUnsafePositionAndRetainsFailures(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	putPreviewFixture(t, l, "Photos/a.png")
	putPreviewFixture(t, l, "Photos/b.png")
	if _, err := l.Put(context.Background(), "Photos/broken.png", []byte("damaged")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetPhotoPreparation(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	s := waitPreparation(t, l, "partial")
	if s.Ready != 2 || s.Failed != 1 {
		t.Fatalf("counters: %+v", s)
	}
	f, _ := l.Metadata(context.Background(), "Photos/broken.png")
	key, _ := thumbnailKey(l.vault, f, 320)
	if err := l.photoFailure(key); err == nil {
		t.Fatal("failure not persisted")
	}
	if err := os.RemoveAll(l.thumbnailDir()); err != nil {
		t.Fatal(err)
	}
	l.photoPrepMu.Lock()
	l.photoPrep.Position = 999 // simulate an old/crashed checkpoint after reorder
	if err := l.savePhotoPreparationLocked(); err != nil {
		t.Fatal(err)
	}
	l.photoPrepLoaded = false
	l.photoPrepMu.Unlock()
	l.ResumePhotoPreparation(context.Background())
	s = waitPreparation(t, l, "partial")
	if s.Ready != 2 || s.Failed != 1 || s.Position != 3 {
		t.Fatalf("restart skipped work: %+v", s)
	}
	if _, err := l.SetPhotoPreparation(context.Background(), "retry"); err != nil {
		t.Fatal(err)
	}
	s = waitPreparation(t, l, "partial")
	if s.Ready != 2 || s.Failed != 1 {
		t.Fatalf("retry counters: %+v", s)
	}
}

func TestPhotoSkippedCacheIsNeverReady(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	putPreviewFixture(t, l, "Photos/a.png")
	old := thumbnailMaxBytes
	thumbnailMaxBytes = 1
	defer func() { thumbnailMaxBytes = old }()
	if _, err := l.SetPhotoPreparation(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	s := waitPreparation(t, l, "partial")
	if s.Ready != 0 || s.Failed != 1 {
		t.Fatalf("skipped cache counted as ready: %+v", s)
	}
	thumbnailMaxBytes = old
	if _, err := l.SetPhotoPreparation(context.Background(), "retry"); err != nil {
		t.Fatal(err)
	}
	s = waitPreparation(t, l, "complete")
	if s.Ready != 1 || s.Failed != 0 {
		t.Fatalf("retry did not recover: %+v", s)
	}
}

func TestPhotoCheckpointFailureIsVisible(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	putPreviewFixture(t, l, "Photos/a.png")
	if err := os.Mkdir(l.photoPreparationPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := l.SetPhotoPreparation(context.Background(), "start")
	if err == nil || s.Status != "paused_error" || s.Error == "" {
		t.Fatalf("hidden checkpoint failure: %+v %v", s, err)
	}
}

func TestPhotoImportPauseCoversGapsAndCacheFailureRecordIsPrivate(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	end := l.BeginPhotoStorageWork()
	if !l.photoStorageBusy() {
		t.Fatal("import not busy without stages")
	}
	end()
	end()
	if l.photoStorageBusy() {
		t.Fatal("import lease leaked")
	}
	if err := l.savePhotoFailure("test-key", errors.New("private-filename.png")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(l.photoFailurePath("test-key"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private-filename.png")) {
		t.Fatal("unencrypted failure")
	}
	if filepath.Base(l.photoFailurePath("test-key")) != "test-key.enc" {
		t.Fatal("unexpected path")
	}
}

func TestPhotoReadinessDropsAfterEviction(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	f := putPreviewFixture(t, l, "Photos/a.png")
	if _, err := l.SetPhotoPreparation(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	waitPreparation(t, l, "complete")
	key, _ := thumbnailKey(l.vault, f, 320)
	if err := os.Remove(filepath.Join(l.thumbnailDir(), key+".enc")); err != nil {
		t.Fatal(err)
	}
	thumbnailCacheEpoch.Add(1)
	s := waitPreparation(t, l, "partial")
	if s.Ready != 0 {
		t.Fatalf("eviction still counted: %+v", s)
	}
}
