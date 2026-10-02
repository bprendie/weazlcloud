package library

import (
	"context"
	"fmt"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"testing"
)

func TestPhotoCollections201MembersAndHiddenOriginal(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	folder, err := l.SavePhotoCollection(ctx, catalog.CollectionFolder{Title: "Phone"})
	if err != nil {
		t.Fatal(err)
	}
	album, err := l.SavePhotoAlbum(ctx, catalog.Album{Title: "Album", ParentID: folder.ID, ParentSet: true})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 201; i++ {
		name := fmt.Sprintf("Photos/%03d.jpg", i)
		if err = l.catalog.Put(catalog.File{Path: name, Hash: fmt.Sprint(i), Size: 1, Snap: "fixture"}); err != nil {
			t.Fatal(err)
		}
		f, _ := l.catalog.Get(name)
		ids = append(ids, f.EntryID)
	}
	l.buildPhotoIndex()
	album, err = l.ChangePhotoAlbumMembers(ctx, album.ID, album.Revision, ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	header, err := l.PhotoAlbumHeader(ctx, album.ID, false)
	if err != nil || header.Count != 201 || len(header.AssetIDs) != 200 || header.ParentID != folder.ID {
		t.Fatalf("header: %+v %v", header, err)
	}
	first, err := l.PhotoAlbumMemberships(ctx, album.ID, "", 200, false)
	if err != nil || len(first.AssetIDs) != 200 || !first.HasMore {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := l.PhotoAlbumMemberships(ctx, album.ID, first.NextCursor, 200, false)
	if err != nil || len(second.AssetIDs) != 1 || second.HasMore {
		t.Fatalf("second: %+v %v", second, err)
	}
	commit := catalog.PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: "opaque", SourceRevision: "1", Hidden: true, OpaqueOriginal: true, AlbumIDs: []string{album.ID}}
	for i, name := range []string{"camera.raw.opaque", "motion.mov"} {
		from := ".weazl-mobile-pending/hidden/" + name
		if err = l.catalog.Put(catalog.File{Path: from, Size: 5, Hash: name, Snap: "fixture"}); err != nil {
			t.Fatal(err)
		}
		part := catalog.PhotoIngestFile{ID: "original", From: from, To: "Photos/" + name, Size: 5, Hash: name, MediaType: "application/octet-stream"}
		if i == 1 {
			part.ID, part.MediaType = "motion", "video/quicktime"
		}
		commit.Files = append(commit.Files, part)
	}
	file, err := l.CommitPhotoIngest(ctx, commit)
	if err != nil {
		t.Fatal(err)
	}
	visible, err := l.PhotoAlbumHeader(ctx, album.ID, false)
	if err != nil || visible.Count != 201 {
		t.Fatalf("hidden leaked: %+v %v", visible, err)
	}
	hidden, err := l.PhotoAlbumHeader(ctx, album.ID, true)
	if err != nil || hidden.Count != 1 {
		t.Fatalf("hidden missing: %+v %v", hidden, err)
	}
	item, err := l.PhotoDetail(ctx, file.EntryID, true)
	if err != nil || len(item.Components) != 2 || item.CapturedAt != nil || item.CaptureSource != "" {
		t.Fatalf("opaque projection: %+v %v", item, err)
	}
	state, err := l.PhotoProcessingState(ctx, file.EntryID)
	if err != nil || state != "unsupported" {
		t.Fatalf("preview: %s %v", state, err)
	}
	for _, part := range commit.Files {
		f, _ := l.catalog.Get(part.To)
		if !f.Hidden {
			t.Fatal("pair Hidden not atomic")
		}
	}
	if photoPreviewable(file.Path) || photoPreviewable("Photos/a.dng") {
		t.Fatal("unsupported original previewable")
	}
}

func TestPhotoSourcesCommitGuardDeniesPublication(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	called := false
	outcomes, err := l.ImportPhotoSourcesGuarded(context.Background(), "phone", []catalog.SourceOperation{{OperationID: "one", Namespace: "test", SourceID: "album", SourceRevision: "1", Kind: "album", Title: "Private"}}, false, func(publish func() error) error { called = true; return catalog.ErrConflict })
	if !called || err != catalog.ErrConflict || len(outcomes) != 0 || len(l.catalog.Albums()) != 0 {
		t.Fatalf("guard published: %+v %v", outcomes, err)
	}
}
