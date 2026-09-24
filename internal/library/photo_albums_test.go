package library

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type albumBackend struct {
	isolatedLegacy
	reads int
}

func (b *albumBackend) ReadRange(ctx context.Context, ref catalog.Reference, offset, length int64, w io.Writer) error {
	b.reads++
	return b.isolatedLegacy.ReadRange(ctx, ref, offset, length, w)
}

func TestPhotoAlbumsLiveMembershipMetadataAndCache(t *testing.T) {
	root := t.TempDir()
	v := vault.New(filepath.Join(root, "vault.json"), filepath.Join(root, "node.key"))
	if err := v.Forge([]byte("album-test"), []byte("album-test")); err != nil {
		t.Fatal(err)
	}
	l := New(filepath.Join(root, "library"), filepath.Join(root, "catalog.enc"), v)
	backend := &albumBackend{isolatedLegacy: isolatedLegacy{root: filepath.Join(root, "library")}}
	l.backend = backend
	ctx := context.Background()
	put := func(name, body string) {
		t.Helper()
		if _, err := l.Put(ctx, name, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	put(PhotosRoot+"Trip/pic.jpg", "same photo")
	put(PhotosRoot+"Trip/pic.jpg.json", `{"title":"not an album","photoTakenTime":{"timestamp":"123"}}`)
	put(PhotosRoot+"Trip/metadata.json", `{"albumData":{"title":"Summer <2020>","description":"Family trip"}}`)
	put(PhotosRoot+"Photos from 2020/pic.jpg", "same photo")
	put(PhotosRoot+"Photos from 2020/metadata.json", `{"title":"Photos from 2020"}`)
	put(PhotosRoot+"Broken/metadata.json", "invalid json")
	put(PhotosRoot+"Huge/metadata.json", strings.Repeat("x", albumMetadataLimit+1))
	put(PhotosRoot+"Other/vacation.mp4", "video")
	put("Other user path/photo.jpg", "unrelated")
	if err := l.Mkdir(ctx, PhotosRoot+"Empty"); err != nil {
		t.Fatal(err)
	}
	get := func() map[string]PhotoAlbum {
		t.Helper()
		albums, err := l.PhotoAlbums(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]PhotoAlbum{}
		for _, a := range albums {
			result[a.Path] = a
		}
		return result
	}
	albums := get()
	if len(albums) != 5 {
		t.Fatalf("albums %+v", albums)
	}
	trip := albums[PhotosRoot+"Trip"]
	if trip.Title != "Summer <2020>" || trip.Count != 1 || trip.Description != "Family trip" || trip.Cover != PhotosRoot+"Trip/pic.jpg" {
		t.Fatalf("trip %+v", trip)
	}
	if !albums[PhotosRoot+"Broken"].MetadataWarning || !albums[PhotosRoot+"Huge"].MetadataWarning {
		t.Fatal("missing metadata fallback")
	}
	if albums[PhotosRoot+"Other"].Count != 1 || albums[PhotosRoot+"Empty"].Count != 0 {
		t.Fatal("video/empty album mismatch")
	}
	reads := backend.reads
	get()
	if backend.reads != reads {
		t.Fatal("warm album lookup reread metadata")
	}
	l.albumMetadata = nil // A new process starts with an empty in-memory cache.
	get()
	if backend.reads != reads {
		t.Fatal("restart reread cached metadata")
	}
	raw, err := os.ReadFile(l.albumCachePath())
	if err != nil || strings.Contains(string(raw), "Summer <2020>") {
		t.Fatal("album cache not encrypted")
	}
	put(PhotosRoot+"Trip/second.jpg", "from next ZIP")
	if get()[PhotosRoot+"Trip"].Count != 2 {
		t.Fatal("split import did not extend album")
	}
	put(PhotosRoot+"Trip/metadata.json", `{"title":"Renamed album"}`)
	if get()[PhotosRoot+"Trip"].Title != "Renamed album" {
		t.Fatal("stale metadata cache")
	}
	if err := l.Delete(PhotosRoot + "Trip/pic.jpg"); err != nil {
		t.Fatal(err)
	}
	if get()[PhotosRoot+"Trip"].Count != 1 {
		t.Fatal("trash still in album")
	}
	if err := l.Delete(PhotosRoot + "Trip"); err != nil {
		t.Fatal(err)
	}
	if _, ok := get()[PhotosRoot+"Trip"]; ok {
		t.Fatal("deleted album still listed")
	}
	v.Lock()
	if _, err := l.PhotoAlbums(ctx); err == nil {
		t.Fatal("locked vault exposed albums")
	}
}

func TestAlbumMetadataShapes(t *testing.T) {
	for _, raw := range []string{`{"title":"Album","description":"desc"}`, `{"albumData":{"title":"Album","description":"desc"}}`} {
		m := parseAlbumMetadata([]byte(raw))
		if !m.Valid || m.Title != "Album" || m.Description != "desc" {
			t.Fatalf("%+v", m)
		}
	}
	if parseAlbumMetadata([]byte(`{"title":"pic.jpg","photoTakenTime":{}}`)).Valid {
		t.Fatal("photo sidecar became an album")
	}
}
