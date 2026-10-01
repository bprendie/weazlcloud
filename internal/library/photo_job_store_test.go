package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

func TestPhotoJobsPersistEncryptedAndResumePerAsset(t *testing.T) {
	first := newPhotoIndexTestLibrary(t)
	putPreviewFixture(t, first, "Photos/Private album/photo.png")
	if _, err := first.SetPhotoPreparation(context.Background(), "start"); err != nil {
		t.Fatal(err)
	}
	waitPreparation(t, first, "complete")
	storePath := first.photoJobStorePath()
	raw, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Private album") || strings.Contains(string(raw), "photo.png") {
		t.Fatal("photo job store contains plaintext owner metadata")
	}

	second := New(first.repo, filepath.Join(filepath.Dir(first.repo), "catalog.enc"), first.vault)
	second.backend = &isolatedLegacy{root: filepath.Join(filepath.Dir(first.repo), "library")}
	t.Cleanup(func() {
		stopPhotoIndexSaveForTest(second)
	})
	second.ResumePhotoPreparation(context.Background())
	waitPreparation(t, second, "complete")
}

func TestExplicitResumePromotesAutomaticPreviewQueueToFullBackfill(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	first := putPreviewFixture(t, l, "Photos/Phone/first.png")
	putPreviewFixture(t, l, "Photos/Archive/second.png")
	l.photoAutoDisabled = false
	l.queuePhotoIngestFiles([]catalog.File{first})
	auto := waitPreparation(t, l, "complete")
	if !auto.AutoOnly || auto.Total != 1 || auto.Ready != 1 {
		t.Fatalf("automatic upload preparation=%+v", auto)
	}
	if _, err := l.SetPhotoPreparation(ctx, "resume"); err != nil {
		t.Fatal(err)
	}
	full := waitPreparation(t, l, "complete")
	if full.AutoOnly || full.Total != 2 || full.Ready != 2 || full.Failed != 0 {
		t.Fatalf("explicit full-library preparation=%+v", full)
	}
}

func TestPhotoUploadAutomaticallyQueuesOnlyNewAsset(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	l.photoAutoDisabled = false
	putPreviewFixture(t, l, "Photos/New phone image/photo.png")
	status := waitPreparation(t, l, "complete")
	if status.Total != 1 || status.Ready != 1 || !status.AutoOnly {
		t.Fatalf("automatic ingest status=%+v", status)
	}
	file, err := l.Metadata(context.Background(), "Photos/New phone image/photo.png")
	if err != nil {
		t.Fatal(err)
	}
	key, err := thumbnailKey(l.vault, file, 320)
	if err != nil {
		t.Fatal(err)
	}
	if !l.validCachedPreview(key) {
		t.Fatal("upload did not produce a cached grid preview")
	}
}

func TestPhotoJobStoreRejectsDamagedCiphertext(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	if err := os.WriteFile(l.photoJobStorePath(), []byte("not an encrypted queue"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.syncPhotoJobs(nil, false); err == nil {
		t.Fatal("damaged queue was silently accepted")
	}
}

func TestPhotoJobLeaseIsRecoveredByNewLibraryInstance(t *testing.T) {
	first := newPhotoIndexTestLibrary(t)
	file := putPreviewFixture(t, first, "Photos/recover.png")
	if _, err := first.syncPhotoJobs([]photoJobFile{{id: file.EntryID, revision: file.Revision}}, false); err != nil {
		t.Fatal(err)
	}
	if jobs, err := first.leasePhotoJobs("old-worker", 1); err != nil || len(jobs) != 1 {
		t.Fatalf("first lease=%+v err=%v", jobs, err)
	}
	second := New(first.repo, filepath.Join(filepath.Dir(first.repo), "catalog.enc"), first.vault)
	jobs, err := second.leasePhotoJobs("new-worker", 1)
	if err != nil || len(jobs) != 1 || jobs[0].LeaseOwner != "new-worker" {
		t.Fatalf("recovered lease=%+v err=%v", jobs, err)
	}
}

func TestPhotoJobProgressIsAggregatedForThePreparationStatus(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	file := putPreviewFixture(t, l, "Photos/rendering.png")
	job := photos.NewMediaJob(l.ownerID, file.EntryID, file.Revision, photoJobOperation, thumbnailRenderer, 2)
	if _, err := l.syncPhotoJobs([]photoJobFile{{id: file.EntryID, revision: file.Revision}}, false); err != nil {
		t.Fatal(err)
	}
	if jobs, err := l.leasePhotoJobs("progress-worker", 1); err != nil || len(jobs) != 1 {
		t.Fatalf("lease=%+v err=%v", jobs, err)
	}
	l.setPhotoJobProgress(job.ID, "progress-worker", photoReadProgress(50, 100))
	working, progress := l.photoJobProgress()
	if working != 1 || progress != 45 {
		t.Fatalf("working=%d progress=%d", working, progress)
	}
}
