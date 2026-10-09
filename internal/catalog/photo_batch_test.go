package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPhotoBatchesAtomicSaveAndConflictIsolation(t *testing.T) {
	c := testCatalog(t)
	files := []File{}
	for i := 0; i < 4; i++ {
		files = append(files, File{Path: fmt.Sprintf(".weazl-mobile-pending/%d", i), Size: 4, Hash: fmt.Sprint(i), Snap: "snapshot"})
	}
	version := c.Version()
	rows := c.PutPhotoComponents(files)
	if c.Version() != version+1 {
		t.Fatal("components did not share one save")
	}
	for _, r := range rows {
		if r.Err != nil || r.File.EntryID == "" {
			t.Fatal(r)
		}
	}
	commits := []PhotoIngestCommit{}
	for i := 0; i < 2; i++ {
		parts := []PhotoIngestFile{}
		for j, id := range []string{"original", "motion"} {
			f := rows[2*i+j].File
			parts = append(parts, PhotoIngestFile{ID: id, From: f.Path, To: fmt.Sprintf("Photos/%d/%s.jpg", i, id), Hash: f.Hash, Size: f.Size})
		}
		commits = append(commits, PhotoIngestCommit{DeviceID: "device", DeviceAssetID: fmt.Sprint(i), SourceRevision: "1", Hidden: i == 1, Files: parts})
	}
	// A bad member cannot poison healthy pair publications.
	commits = append([]PhotoIngestCommit{{Files: []PhotoIngestFile{{ID: "invalid"}}}}, commits...)
	version = c.Version()
	results := c.CommitPhotoIngestBatch(commits)
	if c.Version() != version+1 || results[0].Err == nil || results[1].Err != nil || results[2].Err != nil {
		t.Fatalf("batch results: %+v", results)
	}
	for _, r := range results[1:] {
		if len(r.File.PhotoComponents) != 2 {
			t.Fatal("lost Live Photo pair")
		}
		found := false
		for _, f := range c.All() {
			if f.EntryID == r.File.PhotoComponents[1].AssetID {
				found = f.PhotoParentID == r.File.EntryID && f.Hidden == r.File.Hidden
			}
		}
		if !found {
			t.Fatal("lost motion parent/visibility")
		}
	}
	reloaded := New(c.path, c.vault)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.List()) != 4 {
		t.Fatal("durable batch differs")
	}
	// Disk failure must not publish any new row or revision in memory.
	c.path = filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(c.path, 0700); err != nil {
		t.Fatal(err)
	}
	version = c.Version()
	failed := c.PutPhotoComponents([]File{{Path: ".weazl-mobile-pending/new", Size: 1, Hash: "x", Snap: "s"}})
	if failed[0].Err == nil || c.Version() != version {
		t.Fatal("failed write published")
	}
	if _, ok := c.Get(".weazl-mobile-pending/new"); ok {
		t.Fatal("failed row visible")
	}
}

func BenchmarkPhotoComponentCatalog24K(b *testing.B) {
	for _, batch := range []bool{false, true} {
		b.Run(fmt.Sprintf("batch=%v", batch), func(b *testing.B) {
			c := testCatalog(b)
			rows := make([]File, 24000)
			for i := range rows {
				rows[i] = File{EntryID: fmt.Sprintf("entry-%d", i), Revision: 1, Path: fmt.Sprintf("Drive/%d", i), Size: 1024, Hash: "hash", Snap: "snapshot", Present: true}
			}
			if err := c.saveFilesLocked(rows); err != nil {
				b.Fatal(err)
			}
			c.files = rows
			files := make([]File, 8)
			for i := range files {
				files[i] = File{Path: fmt.Sprintf(".weazl-mobile-pending/%d", i), Size: 1024, Hash: "hash", Snap: "snapshot"}
			}
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if batch {
					for _, r := range c.PutPhotoComponents(files) {
						if r.Err != nil {
							b.Fatal(r.Err)
						}
					}
				} else {
					for _, f := range files {
						if err := c.Put(f); err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}

func TestPhotoPublishBatchDiskFailurePreservesAlbumsAndSources(t *testing.T) {
	c := testCatalog(t)
	album, err := c.SaveAlbum(Album{Title: "Phone album"})
	if err != nil {
		t.Fatal(err)
	}
	f := c.PutPhotoComponents([]File{{Path: ".weazl-mobile-pending/a", Size: 4, Hash: "hash", Snap: "snapshot"}})[0]
	if f.Err != nil {
		t.Fatal(f.Err)
	}
	commit := PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: SafeSourceKey("ios", "photo"), SourceNamespace: "ios", SourceAssetID: "photo", SourceRevision: "1", AlbumIDs: []string{album.ID}, Files: []PhotoIngestFile{{ID: "original", From: f.File.Path, To: "Photos/a.jpg", Hash: "hash", Size: 4}}}
	originalPath := c.path
	c.path = filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(c.path, 0700); err != nil {
		t.Fatal(err)
	}
	version := c.Version()
	result := c.CommitPhotoIngestBatch([]PhotoIngestCommit{commit})
	if result[0].Err == nil || c.Version() != version {
		t.Fatal("failed publish became visible")
	}
	if _, ok := c.Get("Photos/a.jpg"); ok {
		t.Fatal("failed photo visible")
	}
	if len(c.Albums()[0].AssetIDs) != 0 {
		t.Fatal("failed album change visible")
	}
	if _, err := c.SourceMapping("phone", "ios", "photo"); err == nil {
		t.Fatal("failed source change visible")
	}
	c.path = originalPath
	result = c.CommitPhotoIngestBatch([]PhotoIngestCommit{commit})
	if result[0].Err != nil {
		t.Fatal(result[0].Err)
	}
	if len(c.Albums()[0].AssetIDs) != 1 {
		t.Fatal("album not committed")
	}
	if _, err := c.SourceMapping("phone", "ios", "photo"); err != nil {
		t.Fatal(err)
	}
}
