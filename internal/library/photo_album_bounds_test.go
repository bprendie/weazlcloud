package library

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func TestPhotoAlbumHeadersBoundMembersAndPreserveFullReconciliation(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	// Metadata-only fixture: no mass upload, rendering or original reads.
	files := []catalog.File{{EntryID: "private-folder", Path: "Photos/Private", Folder: true, Hidden: true, Present: true, Revision: 1}}
	ids := []string{}
	for i := 0; i < 203; i++ {
		name := fmt.Sprintf("Photos/visible-%03d.jpg", i)
		if i == 202 {
			name = "Photos/Private/secret.jpg"
		}
		id := fmt.Sprintf("asset-%03d", i)
		files = append(files, catalog.File{EntryID: id, Path: name, Present: true, Revision: 1, Size: 10})
		ids = append(ids, id)
	}
	raw, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := l.vault.Wrap(raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := cryptox.AtomicWrite(filepath.Join(filepath.Dir(l.repo), "catalog.enc"), wrapped, 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	album, err := l.SavePhotoAlbum(ctx, catalog.Album{Title: "Large album", AssetIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	header, err := l.PhotoAlbumHeader(ctx, album.ID, false)
	if err != nil || header.Count != 202 || len(header.AssetIDs) != 200 {
		t.Fatalf("unbounded or incomplete album header=%+v %v", header, err)
	}
	albums, err := l.PhotoAlbums(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, header := range albums {
		if header.ID == album.ID {
			found = true
			if header.Count != 202 || len(header.AssetIDs) != 200 {
				t.Fatal("album list did not bound memberships")
			}
		}
	}
	if !found {
		t.Fatal("custom album missing")
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := l.PhotoAlbumMemberships(ctx, album.ID, cursor, 100, false)
		if err != nil || len(page.AssetIDs) > 100 {
			t.Fatalf("membership page=%+v %v", page, err)
		}
		for _, id := range page.AssetIDs {
			if seen[id] || id == ids[202] {
				t.Fatal("duplicate or hidden member in normal reconciliation")
			}
			seen[id] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 202 {
		t.Fatalf("bounded headers lost members: got=%d", len(seen))
	}
	header, err = l.PhotoAlbumHeader(ctx, album.ID, true)
	if err != nil || header.Count != 1 || len(header.AssetIDs) != 1 || header.AssetIDs[0] != ids[202] {
		t.Fatalf("hidden context=%+v %v", header, err)
	}
}
