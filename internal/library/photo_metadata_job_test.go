package library

import (
	"context"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"os"
	"strings"
	"testing"
	"time"
)

func waitMetadata(t *testing.T, l *Library) PhotoMetadataJob {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		state, err := l.PhotoMetadataStatus()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(state.Status, "complete") || state.Status == "paused_error" {
			return state
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("metadata job did not finish")
	return PhotoMetadataJob{}
}
func TestMetadataJobDryRunBatchResilienceAndEncryption(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"photo.jpg", "broken.jpg", "unknown.png"} {
		_, err := l.Put(ctx, "Photos/Trip/"+name, []byte(name))
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _ = l.Put(ctx, "Photos/Trip/photo.jpg.supplemental-metadata.json", []byte(`{"title":"photo.jpg","photoTakenTime":{"timestamp":"1365152400"}}`))
	_, _ = l.Put(ctx, "Photos/Trip/broken.jpg.json", []byte(`{broken`))
	before, _ := os.ReadFile(l.catalogPathForTest())
	_, err := l.SetPhotoMetadataJob(ctx, "dry-run", PhotoMetadataOptionsJob{SidecarsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	dry := waitMetadata(t, l)
	if dry.Updated != 1 || dry.Failed != 1 || dry.Unresolved != 1 {
		t.Fatalf("dry %+v", dry)
	}
	after, _ := os.ReadFile(l.catalogPathForTest())
	if string(before) != string(after) {
		t.Fatal("dry run changed catalog")
	}
	_, err = l.SetPhotoMetadataJob(ctx, "start", PhotoMetadataOptionsJob{SidecarsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	applied := waitMetadata(t, l)
	if applied.Updated != 1 || applied.Failed != 1 || applied.Unresolved != 1 {
		t.Fatal(applied)
	}
	file, _ := l.Metadata(ctx, "Photos/Trip/photo.jpg")
	if file.CaptureTime == nil || file.CaptureTime.Year() != 2013 {
		t.Fatal("date not repaired", file)
	}
	raw, _ := os.ReadFile(l.metadataJobPath())
	if strings.Contains(string(raw), "photo.jpg") || strings.Contains(string(raw), "takeout-photoTakenTime") {
		t.Fatal("plaintext metadata checkpoint")
	}
	l.metadataMu.Lock()
	l.metadataJob = nil
	l.metadataMu.Unlock()
	loaded, err := l.PhotoMetadataStatus()
	if err != nil || loaded.Updated != 1 {
		t.Fatal("checkpoint reload", loaded, err)
	}
	_, _ = l.SetPhotoMetadataJob(ctx, "start", PhotoMetadataOptionsJob{SidecarsOnly: true})
	repeated := waitMetadata(t, l)
	if repeated.Updated != 0 || repeated.Unchanged != 1 {
		t.Fatal("non-idempotent repeat", repeated)
	}
	applyCheckpoint, _ := os.ReadFile(l.metadataJobPath())
	if _, err = l.SetPhotoMetadataJob(ctx, "dry-run", PhotoMetadataOptionsJob{SidecarsOnly: true}); err != nil {
		t.Fatal(err)
	}
	_ = waitMetadata(t, l)
	retained, _ := os.ReadFile(l.metadataJobPath())
	if string(applyCheckpoint) != string(retained) {
		t.Fatal("inspection overwrote the apply checkpoint")
	}
	l.metadataMu.Lock()
	l.metadataJob = nil
	l.metadataMu.Unlock()
	loaded, err = l.PhotoMetadataStatus()
	if err != nil || !loaded.Options.DryRun {
		t.Fatal("latest dry-run state did not reload", loaded, err)
	}
}
func (l *Library) catalogPathForTest() string {
	return strings.TrimSuffix(l.metadataJobPath(), ".weazl-photo-metadata.enc") + "catalog.enc"
}

func TestMetadataConcurrentCorrectionAndMissingSourceAreIndependent(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"a.jpg", "b.jpg"} {
		_, _ = l.Put(ctx, "Photos/"+name, []byte(name))
		_, _ = l.Put(ctx, "Photos/"+name+".json", []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`))
	}
	resolver, err := l.newMetadataResolver(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := l.Metadata(ctx, "Photos/a.jpg")
	result := l.resolveMetadataEntry(ctx, resolver, PhotoMetadataEntry{ID: file.EntryID, Hash: file.Hash, Path: file.Path, Revision: file.Revision}, PhotoMetadataOptionsJob{})
	corrected := time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = l.SetPhotoCapture(ctx, file.Path, catalog.CaptureMetadata{Time: &corrected, Source: "user", UserCorrected: true})
	if err != nil {
		t.Fatal(err)
	}
	l.metadataMu.Lock()
	l.metadataJob = &PhotoMetadataJob{Version: 1, Parser: "capture-v2", Status: "running", Entries: []PhotoMetadataEntry{result.entry}}
	l.metadataMu.Unlock()
	if err = l.commitMetadataResults(ctx, []metadataResolved{result}, false); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Metadata(ctx, file.Path)
	if !got.CaptureTime.Equal(corrected) {
		t.Fatal("manual correction overwritten")
	}
	l.metadataMu.Lock()
	category := l.metadataJob.Entries[0].Category
	l.metadataMu.Unlock()
	if category != "concurrent_change" {
		t.Fatal("conflict not recorded")
	}
	_, _ = l.SetPhotoMetadataJob(ctx, "retry", PhotoMetadataOptionsJob{})
	state := waitMetadata(t, l)
	if state.Unchanged != 1 {
		t.Fatal("correction not retained", state)
	}
}
