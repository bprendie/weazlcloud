package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func lookupKey(data []byte) PhotoContentKey {
	s := sha256.Sum256(data)
	return PhotoContentKey{hex.EncodeToString(s[:]), int64(len(data))}
}
func TestPhotoContentLookupVisibilityAndMutations(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	data := []byte("same exact original")
	key := lookupKey(data)
	for _, p := range []string{"Photos/a.jpg", "Photos/private/b.jpg", "Documents/out.jpg", ".weazl-mobile-pending/staged.jpg", "Photos/sidecar.json"} {
		if _, err := l.Put(ctx, p, data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.SetPhotoFolderHidden(ctx, "Photos/private", true); err != nil {
		t.Fatal(err)
	}
	check := func(hidden bool, count int) PhotoContentResult {
		t.Helper()
		page, err := l.PhotoContentLookup(ctx, []PhotoContentKey{key}, hidden)
		if err != nil || len(page.Results) != 1 {
			t.Fatalf("lookup: %+v %v", page, err)
		}
		r := page.Results[0]
		if r.MatchCount != count || r.Exists != (count > 0) {
			t.Fatalf("matches=%+v want=%d", r, count)
		}
		return r
	}
	check(false, 1)
	check(true, 2)
	f, err := l.Metadata(ctx, "Photos/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	archived := true
	if _, err := l.catalog.SetPhotoFlags([]catalog.File{f}, nil, &archived); err != nil {
		t.Fatal(err)
	}
	l.publishChange(Change{Kind: "photo-visibility", Paths: []string{f.Path}})
	if !check(false, 1).Matches[0].Archived {
		t.Fatal("archived original was lost")
	}
	upper := key
	upper.SHA256 = strings.ToUpper(key.SHA256)
	wrong := key
	wrong.Size++
	page, err := l.PhotoContentLookup(ctx, []PhotoContentKey{upper, wrong, key}, false)
	if err != nil || !page.Results[0].Exists || page.Results[1].Exists || !page.Results[2].Exists || page.Results[0].SHA256 != key.SHA256 {
		t.Fatalf("ordered exact matches: %+v %v", page, err)
	}
	if err = l.Delete("Photos/a.jpg"); err != nil {
		t.Fatal(err)
	}
	check(false, 0)
	check(true, 1)
	if err = l.Restore(ctx, "Photos/a.jpg"); err != nil {
		t.Fatal(err)
	}
	check(false, 1)
	if err = l.Rename(ctx, "Photos/a.jpg", "Documents/moved.jpg"); err != nil {
		t.Fatal(err)
	}
	check(false, 0)
	if err = l.Rename(ctx, "Documents/moved.jpg", "Photos/back.jpg"); err != nil {
		t.Fatal(err)
	}
	check(false, 1)
	if _, err = l.Put(ctx, "Photos/back.jpg", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	check(false, 0)
	if _, err = l.SetPhotoFolderHidden(ctx, "Photos/private", false); err != nil {
		t.Fatal(err)
	}
	check(false, 1)
	l.ForgetVaultSession()
	if l.photoContentIndex != nil {
		t.Fatal("checksum cache retained after vault forget")
	}
	l.vault.Lock()
	if _, err = l.PhotoContentLookup(ctx, []PhotoContentKey{key}, true); !errors.Is(err, vault.ErrLocked) {
		t.Fatal(err)
	}
}
func TestPhotoContentLookupPairOpaqueAndBounds(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	still, motion := []byte("still original"), []byte("motion original")
	files := []catalog.PhotoIngestFile{}
	for i, body := range [][]byte{still, motion} {
		id, ext := "original", ".opaque"
		if i == 1 {
			id, ext = "motion", ".mov"
		}
		from := ".weazl-mobile-pending/" + id
		to := "Photos/live" + ext
		if _, err := l.Put(ctx, from, body); err != nil {
			t.Fatal(err)
		}
		key := lookupKey(body)
		files = append(files, catalog.PhotoIngestFile{ID: id, From: from, To: to, Hash: key.SHA256, Size: key.Size})
	}
	if _, err := l.catalog.CommitPhotoIngest(catalog.PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: "asset", SourceRevision: "1", OpaqueOriginal: true, Files: files}); err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoContentLookup(ctx, []PhotoContentKey{lookupKey(still), lookupKey(motion)}, false)
	if err != nil || len(page.Results[0].Matches) != 1 || len(page.Results[1].Matches) != 1 {
		t.Fatalf("pair: %+v %v", page, err)
	}
	a, b := page.Results[0].Matches[0], page.Results[1].Matches[0]
	if a.AssetID != b.AssetID || a.ComponentID != "original" || b.ComponentID != "motion" || a.ComponentAssetID == b.ComponentAssetID {
		t.Fatalf("lost pair identity: %+v %+v", a, b)
	}
	for i := 0; i < 22; i++ {
		if _, err = l.Put(ctx, fmt.Sprintf("Photos/duplicate-%02d.jpg", i), still); err != nil {
			t.Fatal(err)
		}
	}
	page, err = l.PhotoContentLookup(ctx, []PhotoContentKey{lookupKey(still)}, false)
	r := page.Results[0]
	if err != nil || !r.HasMoreMatches || r.MatchCount != 23 || len(r.Matches) != PhotoContentMatchLimit {
		t.Fatalf("unbounded matches: %+v %v", r, err)
	}
	for _, keys := range [][]PhotoContentKey{nil, make([]PhotoContentKey, 201), {{"bad", 1}}, {{strings.Repeat("a", 64), 0}}, {{strings.Repeat("g", 64), 1}}} {
		if _, err = l.PhotoContentLookup(ctx, keys, false); !errors.Is(err, ErrPhotoContentLookup) {
			t.Fatalf("invalid request accepted: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = l.PhotoContentLookup(cancelled, []PhotoContentKey{lookupKey(still)}, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
