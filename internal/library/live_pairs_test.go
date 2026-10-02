package library

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/photos"
)

func TestImportedLivePairPreservesOriginalsAlbumsDatesAndUnlinks(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	still, _ := l.Put(ctx, "Photos/Trip/still.jpg", []byte("still original"))
	motion, _ := l.Put(ctx, "Photos/Trip/motion.mov", []byte("motion original"))
	ordinary, _ := l.Put(ctx, "Photos/Trip/short.mov", []byte("ordinary short video"))
	still, _ = l.catalog.Get(still.Path)
	motion, _ = l.catalog.Get(motion.Path)
	ordinary, _ = l.catalog.Get(ordinary.Path)
	when := time.Date(2018, 2, 3, 4, 5, 6, 0, time.UTC)
	if _, err := l.SetPhotoCapture(ctx, still.Path, catalog.CaptureMetadata{Time: &when, Source: "user", UserCorrected: true}); err != nil {
		t.Fatal(err)
	}
	still, _ = l.catalog.Get(still.Path)
	album, err := l.catalog.SaveAlbum(catalog.Album{Title: "Trip", AssetIDs: []string{motion.EntryID}})
	if err != nil {
		t.Fatal(err)
	}
	paired, err := l.LinkLivePhoto(ctx, still.EntryID, motion.EntryID, still.Revision, motion.Revision, false, false)
	if err != nil || len(paired.Components) != 2 {
		t.Fatalf("pair=%+v %v", paired, err)
	}
	page, err := l.PhotoPage(ctx, 20, "", "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("pair plus short video: %+v %v", page, err)
	}
	for _, id := range []string{still.EntryID, ordinary.EntryID} {
		found := false
		for _, item := range page.Items {
			found = found || item.ID == id
		}
		if !found {
			t.Fatalf("missing %s", id)
		}
	}
	updated, _ := l.catalog.Get(still.Path)
	child, _ := l.catalog.Get(motion.Path)
	if updated.Hash != still.Hash || child.Hash != motion.Hash || updated.CaptureTime == nil || !updated.CaptureTime.Equal(when) || !updated.CaptureUserCorrected {
		t.Fatal("original identity/date changed")
	}
	exports, release, err := l.PreparePhotoExport(ctx, []string{still.EntryID}, false)
	if err != nil || len(exports) != 2 {
		t.Fatal(exports, err)
	}
	release()
	albums := l.catalog.Albums()
	if len(albums) != 1 || len(albums[0].AssetIDs) != 1 || albums[0].AssetIDs[0] != still.EntryID {
		t.Fatal("membership not transferred", albums)
	}
	unlinked, err := l.LinkLivePhoto(ctx, still.EntryID, motion.EntryID, updated.Revision, child.Revision, false, true)
	if err != nil || len(unlinked.Components) != 0 {
		t.Fatal(unlinked, err)
	}
	restored := false
	for _, a := range l.catalog.Albums() {
		if a.ID == album.ID {
			for _, id := range a.AssetIDs {
				restored = restored || id == motion.EntryID
			}
		}
	}
	if !restored {
		t.Fatal("motion album membership lost")
	}
	if _, err := l.LinkLivePhoto(ctx, still.EntryID, motion.EntryID, still.Revision, motion.Revision, false, false); !errors.Is(err, catalog.ErrRevisionMismatch) {
		t.Fatal("stale pair admitted", err)
	}
}

func TestLiveRepairSkipsAmbiguousCandidatesAndIsIdempotent(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	entries := []LivePhotoEntry{}
	for _, fixture := range []struct{ name, identifier, media string }{{"still.jpg", "pair", "image/jpeg"}, {"motion.mov", "pair", "video/quicktime"}, {"short.mov", "", "video/quicktime"}, {"duplicate.jpg", "ambiguous", "image/jpeg"}, {"duplicate2.jpg", "ambiguous", "image/jpeg"}, {"duplicate.mov", "ambiguous", "video/quicktime"}} {
		file, err := l.Put(ctx, "Photos/Trip/"+fixture.name, []byte(fixture.name))
		if err != nil {
			t.Fatal(err)
		}
		file, _ = l.catalog.Get(file.Path)
		entries = append(entries, LivePhotoEntry{ID: file.EntryID, Hash: file.Hash, Path: file.Path, Status: "examined", Identifier: fixture.identifier, MediaType: fixture.media})
	}
	l.liveJob = &LivePhotoJob{Status: "running", Entries: entries, Total: len(entries)}
	l.finishLivePairs(ctx)
	if l.liveJob.Paired != 1 || l.liveJob.Ambiguous != 3 || l.liveJob.Orphan != 1 {
		t.Fatalf("job=%+v", l.liveJob)
	}
	l.liveJob.Status = "running"
	l.finishLivePairs(ctx)
	page, err := l.PhotoPage(ctx, 20, "", "")
	if err != nil || len(page.Items) != 5 {
		t.Fatal("repair duplicated or removed assets", page, err)
	}
	raw, err := l.LivePhotoStatus(true)
	if err != nil || raw.Paired != 1 {
		t.Fatal(raw, err)
	}
	if err := l.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	if err := l.Rename(ctx, "Photos/Trip/motion.mov", "Photos/Private/motion.mov"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.liveMotionFile(ctx, entries[0].ID, false); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("hidden motion leaked", err)
	}
}

func TestHEICRecoveryOnlyRequeuesFailedHEICAndHonorsPause(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	files := []catalog.File{}
	for _, name := range []string{"Photos/a.heic", "Photos/b.jpg"} {
		file, err := l.Put(ctx, name, []byte(name))
		if err != nil {
			t.Fatal(err)
		}
		file, _ = l.catalog.Get(file.Path)
		files = append(files, file)
	}
	if _, err := l.PhotoPage(ctx, 20, "", ""); err != nil {
		t.Fatal(err)
	}
	l.photoJobsMu.Lock()
	if err := l.loadPhotoJobsLocked(); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		job := photos.NewMediaJob(l.ownerID, f.EntryID, f.Revision, photoJobOperation, thumbnailRenderer, 1)
		job.Status = photos.JobFailed
		job.ErrorCategory = "invalid_or_unsupported_media"
		l.photoJobs.Replace(job)
		key, _ := thumbnailKey(l.vault, f, 320)
		if err := l.savePhotoFailure(key, errors.New("legacy rejection")); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.savePhotoJobsLocked(); err != nil {
		t.Fatal(err)
	}
	l.photoJobsMu.Unlock()
	l.photoPrep.Paused = true
	repairedDate := time.Date(2016, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := l.SetPhotoCapture(ctx, files[0].Path, catalog.CaptureMetadata{Time: &repairedDate, Source: "sidecar"}); err != nil {
		t.Fatal(err)
	}
	recovered, err := l.RecoverHEICPreviews(ctx)
	if err != nil || recovered != 1 || !l.photoPrep.Paused {
		t.Fatal(recovered, err)
	}
	recovered, err = l.RecoverHEICPreviews(ctx)
	if err != nil || recovered != 0 {
		t.Fatal("recovery replay", recovered, err)
	}
	for i, f := range files {
		key, _ := thumbnailKey(l.vault, f, 320)
		err := l.photoFailure(key)
		if i == 0 && err != nil || i == 1 && !errors.Is(err, ErrPhotoPreviouslyFailed) {
			t.Fatal("wrong failure record removed", i, err)
		}
	}
}

func TestLiveRepairDoesNotFinishWithLatePendingResource(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	l.liveJob = &LivePhotoJob{Status: "running", Entries: []LivePhotoEntry{{ID: "late", Status: "pending"}}, Total: 1}
	if l.finishLivePairs(context.Background()) || l.liveJob.Status != "running" {
		t.Fatal("late arrival lost", l.liveJob)
	}
	if _, err := l.SetLivePhotoJob(context.Background(), "pause"); err != nil {
		t.Fatal(err)
	}
	l.liveMu.Lock()
	l.liveJob = nil
	l.liveMu.Unlock()
	status, err := l.LivePhotoStatus(true)
	if err != nil || status.Status != "paused" || len(status.Entries) != 1 {
		t.Fatal("durable pause lost", status, err)
	}
	if err := l.queueLiveIngest([]catalog.File{{EntryID: "new", Path: "Photos/new.jpg", Hash: "new", Present: true}}); err != nil {
		t.Fatal(err)
	}
	status, err = l.LivePhotoStatus(true)
	if err != nil || status.Status != "paused" || len(status.Entries) != 1 {
		t.Fatal("upload resumed manual pause", status, err)
	}
}

func TestLiveRepairRevalidatesCollectionAfterMove(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	entries := []LivePhotoEntry{}
	for _, name := range []string{"still.jpg", "motion.mov"} {
		f, err := l.Put(ctx, "Photos/old/"+name, []byte(name))
		if err != nil {
			t.Fatal(err)
		}
		f, _ = l.catalog.Get(f.Path)
		media := "image/jpeg"
		if name == "motion.mov" {
			media = "video/quicktime"
		}
		entries = append(entries, LivePhotoEntry{ID: f.EntryID, Hash: f.Hash, Path: f.Path, Status: "examined", Identifier: "same", MediaType: media})
	}
	if err := l.Rename(ctx, "Photos/old/motion.mov", "Photos/new/motion.mov"); err != nil {
		t.Fatal(err)
	}
	l.liveJob = &LivePhotoJob{Status: "running", Entries: entries, Total: 2}
	l.finishLivePairs(ctx)
	if l.liveJob.Paired != 0 || l.liveJob.Ambiguous != 2 {
		t.Fatal("moved collection paired from stale evidence", l.liveJob)
	}
}
