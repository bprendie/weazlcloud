package photos

import (
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestProjectionKeepsAssetAndAlbumIDsAcrossRename(t *testing.T) {
	capture := time.Date(2013, 4, 5, 6, 7, 8, 0, time.UTC)
	files := []catalog.File{
		{EntryID: "folder-entry", Revision: 2, Path: "Photos/Trip", Folder: true, Present: true},
		{EntryID: "photo-entry", Revision: 3, Path: "Photos/Trip/photo.jpg", Present: true, Hash: "hash", Mtime: time.Now().UTC(), ImportedAt: time.Now().UTC(), CaptureTime: &capture, CaptureSource: "takeout-photoTakenTime"},
	}
	first := BuildProjection("bobp", files, nil)
	if len(first.Assets) != 1 || len(first.Albums) != 1 {
		t.Fatalf("projection=%+v", first)
	}
	if first.Assets[0].ID != "asset:photo-entry" || first.Albums[0].ID != "album:folder-entry" {
		t.Fatalf("unstable IDs: assets=%+v albums=%+v", first.Assets, first.Albums)
	}
	if len(first.Assets[0].AlbumIDs) != 1 || first.Assets[0].AlbumIDs[0] != first.Albums[0].ID {
		t.Fatalf("membership=%+v", first.Assets[0].AlbumIDs)
	}
	renamed := append([]catalog.File(nil), files...)
	renamed[0].Path = "Photos/Road Trip"
	renamed[1].Path = "Photos/Road Trip/photo.jpg"
	second := BuildProjection("bobp", renamed, nil)
	if second.Assets[0].ID != first.Assets[0].ID || second.Albums[0].ID != first.Albums[0].ID {
		t.Fatalf("rename changed IDs: first=%+v second=%+v", first, second)
	}
	report := CompareLegacy("bobp", renamed, second)
	if report.MissingAssets != 0 || report.UnexpectedAssets != 0 || report.PathMismatches != 0 || report.CaptureMismatches != 0 {
		t.Fatalf("comparison=%+v", report)
	}
}

func TestProjectionIncludesTrashRootsAndDuplicateGroups(t *testing.T) {
	capture := time.Date(2021, 2, 3, 4, 5, 6, 0, time.UTC)
	files := []catalog.File{
		{EntryID: "root-entry", Path: "Photos", Folder: true, Present: true},
		{EntryID: "folder-entry", Path: "Photos/Trip", Folder: true, Present: true},
		{EntryID: "photo-entry", Revision: 2, Path: "Photos/Trip/photo.jpg", Present: false, Hash: "same", CaptureTime: &capture, CaptureSource: "takeout-photoTakenTime"},
	}
	projection := BuildProjection("bobp", files, []SourceRoot{{EntryID: "root-entry", Path: "Photos", Included: true}})
	if len(projection.Assets) != 1 || !projection.Assets[0].Trashed || projection.Assets[0].DuplicateGroupID != "duplicate:same" {
		t.Fatalf("projection=%+v", projection)
	}
	if projection.Roots[0].ID != "root:root-entry" || len(projection.Assets[0].AlbumIDs) != 1 {
		t.Fatalf("roots/membership=%+v assets=%+v", projection.Roots, projection.Assets)
	}
	current := projection
	current.Assets[0].Width = 100
	report := CompareProjection("bobp", files, projection.Roots, current)
	if report.MediaMismatches != 1 || report.RootMismatches != 0 || report.MembershipMismatches != 0 {
		t.Fatalf("comparison=%+v", report)
	}
}
