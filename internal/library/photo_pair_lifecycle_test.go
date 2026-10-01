package library

import (
	"context"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoPairDeleteRestoreCopyHiddenAndProjection(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	for _, name := range []string{"Photos/Pair", "Photos/Private"} {
		if err := l.Mkdir(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	parts := []catalog.PhotoIngestFile{}
	for i, name := range []string{"still.jpg", "motion.mov"} {
		from := ".weazl-mobile-pending/pair/" + name
		if _, err := l.Put(ctx, from, []byte(name)); err != nil {
			t.Fatal(err)
		}
		file, _ := l.catalog.Get(from)
		id, kind := "original", "image/jpeg"
		if i == 1 {
			id, kind = "motion", "video/quicktime"
		}
		parts = append(parts, catalog.PhotoIngestFile{ID: id, From: from, To: "Photos/Pair/" + name, Hash: file.Hash, Size: file.Size, MediaType: kind})
	}
	commit := catalog.PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: "pair", SourceRevision: "1", Files: parts}
	file, err := l.CommitPhotoIngest(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	again, err := l.CommitPhotoIngest(ctx, commit)
	if err != nil || again.EntryID != file.EntryID || again.Revision != file.Revision {
		t.Fatalf("commit replay changed asset: %+v %v", again, err)
	}
	page, err := l.PhotoPage(ctx, 20, "", "")
	if err != nil || len(page.Items) != 1 || len(page.Items[0].Components) != 2 {
		t.Fatalf("pair timeline: %+v %v", page, err)
	}
	exports, release, err := l.PreparePhotoExport(ctx, []string{file.EntryID}, false)
	if err != nil || len(exports) != 2 {
		t.Fatalf("pair export lost motion: %+v %v", exports, err)
	}
	release()
	if err := l.Delete(file.Path); err != nil {
		t.Fatal(err)
	}
	page, err = l.PhotoPage(ctx, 20, "", "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("deleted pair leaked: %+v %v", page, err)
	}
	trash, err := l.PhotoTrash(ctx, false)
	if err != nil || len(trash) != 1 {
		t.Fatalf("logical trash=%+v %v", trash, err)
	}
	if err := l.RestorePhoto(ctx, file.EntryID, false); err != nil {
		t.Fatal(err)
	}
	page, err = l.PhotoPage(ctx, 20, "", "")
	if err != nil || len(page.Items) != 1 || len(page.Items[0].Components) != 2 {
		t.Fatalf("pair restore=%+v %v", page, err)
	}
	if err := l.Copy(ctx, "Photos/Pair", "Photos/Copied"); err != nil {
		t.Fatal(err)
	}
	copied, _ := l.catalog.Get("Photos/Copied/still.jpg")
	if copied.EntryID == file.EntryID || len(copied.PhotoComponents) != 2 || copied.PhotoComponents[1].AssetID == file.PhotoComponents[1].AssetID || copied.DeviceID != "" {
		t.Fatalf("copied pair retained source identity: %+v", copied)
	}
	if err := l.Rename(ctx, "Photos/Pair/motion.mov", "Photos/Private/motion.mov"); err != nil {
		t.Fatal(err)
	}
	detail, err := l.PhotoDetail(ctx, file.EntryID)
	if err != nil || len(detail.Components) != 1 {
		t.Fatalf("normal detail enumerated hidden motion: %+v %v", detail, err)
	}
	exports, release, err = l.PreparePhotoExport(ctx, []string{file.EntryID}, false)
	if err != nil || len(exports) != 1 {
		t.Fatalf("normal export shared hidden motion: %+v %v", exports, err)
	}
	release()
}
