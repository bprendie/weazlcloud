package library

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCacheInventoryCannotBlockStatusOrLibraryBrowsing(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	putPreviewFixture(t, l, "Photos/a.png")
	if _, err := l.SetPhotoPreparation(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	waitPreparation(t, l, "complete")
	// Hold actual cache bookkeeping to simulate a slow inventory read. Both
	// status polling and ordinary folder listing must finish before it resumes.
	thumbnailCacheMu.Lock()
	var once sync.Once
	unblock := func() { once.Do(thumbnailCacheMu.Unlock) }
	defer unblock()
	thumbnailCacheEpoch.Add(1)
	status := make(chan photoPreparation, 1)
	go func() { s, _ := l.PhotoPreparation(); status <- s }()
	select {
	case s := <-status:
		if !s.CacheChecking {
			t.Fatal("inventory did not start")
		}
	case <-time.After(time.Second):
		t.Fatal("status waited for cache inventory")
	}
	l.photoJobsMu.Lock()
	var queueOnce sync.Once
	releaseQueue := func() { queueOnce.Do(l.photoJobsMu.Unlock) }
	defer releaseQueue()
	go func() { s, _ := l.PhotoPreparation(); status <- s }()
	select {
	case <-status:
	case <-time.After(time.Second):
		t.Fatal("status waited for queue initialization")
	}
	browsed := make(chan error, 1)
	go func() { _, err := l.ListFolderPage(context.Background(), "", "name", false, 100, ""); browsed <- err }()
	select {
	case err := <-browsed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("folder browsing waited for preview inventory")
	}
	l.stopPreviews()
	releaseQueue()
	unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	l.photoPrepMu.Lock()
	defer l.photoPrepMu.Unlock()
	if l.photoCacheChecking {
		t.Fatal("inventory outlived drain")
	}
}
