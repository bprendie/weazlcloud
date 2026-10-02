package desk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestMobileGrabGalleryCompleteAlbumHiddenAndRestart(t *testing.T) {
	dir := t.TempDir()
	us, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := us.Create("owner", "password-test", true)
	if err != nil {
		t.Fatal(err)
	}
	store := capsule.New(filepath.Join(dir, "capsules"))
	h := NewMulti(us, store, nil, "https://grab.test", "", dir)
	shared, err := sharedstore.Open(dir, sharedstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	h.registry.ConfigureShared(shared, true)
	res := h.registry.For(u)
	t.Cleanup(func() { _ = res.Lib.Drain(context.Background()) })
	if err := res.Vault.Forge([]byte("test"), []byte("test")); err != nil {
		t.Fatal(err)
	}
	token, err := us.Login(u)
	if err != nil {
		t.Fatal(err)
	}
	cookieWriter := httptest.NewRecorder()
	us.SetSession(cookieWriter, token)
	cookie := cookieWriter.Result().Cookies()[0]
	request := func(method, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		route := "/api/v1/photos/grabs"
		if method == "GET" {
			route = "/api/v1/grabs/operations"
		}
		r := httptest.NewRequest(method, route, bytes.NewBufferString(body))
		r.AddCookie(cookie)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		if !h.tryMobileGrabs(w, r) {
			t.Fatal("not intercepted")
		}
		return w
	}
	ctx := context.Background()
	ids := make([]string, 0, 201)
	for i := 0; i < 201; i++ {
		f, err := res.Lib.Put(ctx, fmt.Sprintf("Photos/album/%03d.jpg", i), []byte("preserved unsupported original"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.EntryID)
	}
	all, err := res.Lib.CreatePhotoSelection(ctx, library.PhotoSelectionOptions{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	album, err := res.Lib.SavePhotoSelectionAlbum(ctx, all.ID, false, catalog.Album{Title: "Whole album"})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := res.Lib.CreatePhotoSelection(ctx, library.PhotoSelectionOptions{Mode: "all", Filter: library.PhotoSearchOptions{Album: "album:" + album.ID}})
	if err != nil || selection.Count != 201 {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	body := fmt.Sprintf(`{"selection_id":%q,"gate":"open","grabs":2}`, selection.ID)
	minted := request("POST", "whole-album", body)
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint=%d %s", minted.Code, minted.Body.String())
	}
	var view struct{ ID string }
	json.Unmarshal(minted.Body.Bytes(), &view)
	manifest, _, err := store.GallerySession(view.ID, "")
	if err != nil || len(manifest.Items) != 201 {
		t.Fatalf("gallery items=%d err=%v", len(manifest.Items), err)
	}
	// Selection invalidation must not prevent replay of an already frozen result.
	if err := res.Lib.Rename(ctx, "Photos/album/000.jpg", "Photos/moved.jpg"); err != nil {
		t.Fatal(err)
	}
	h.caps = capsule.New(filepath.Join(dir, "capsules"))
	retry := request("POST", "whole-album", body)
	if retry.Code != 201 || !bytes.Equal(retry.Body.Bytes(), minted.Body.Bytes()) {
		t.Fatal("gallery restart created another result")
	}
	if len(h.caps.ListOwner(u.ID)) != 1 {
		t.Fatal("duplicate gallery")
	}
	if err := res.Lib.Mkdir(ctx, "Photos/Private"); err != nil {
		t.Fatal(err)
	}
	if _, err := res.Lib.SetPhotoFolderHidden(ctx, "Photos/Private", true); err != nil {
		t.Fatal(err)
	}
	hidden, err := res.Lib.Put(ctx, "Photos/Private/secret.jpg", []byte("secret original"))
	if err != nil {
		t.Fatal(err)
	}
	privateBody := fmt.Sprintf(`{"ids":[%q],"hidden":true,"gate":"open","grabs":2}`, hidden.EntryID)
	denied := request("POST", "hidden-unconfirmed", privateBody)
	if denied.Code != 400 {
		t.Fatalf("hidden confirmation=%d", denied.Code)
	}
	confirmed := fmt.Sprintf(`{"ids":[%q],"hidden":true,"confirm_hidden":true,"gate":"passphrase","passphrase":"guest-secret","grabs":2}`, hidden.EntryID)
	allowed := request("POST", "hidden-confirmed", confirmed)
	if allowed.Code != 201 {
		t.Fatalf("hidden=%d %s", allowed.Code, allowed.Body.String())
	}
	json.Unmarshal(allowed.Body.Bytes(), &view)
	if _, _, err := h.caps.GallerySession(view.ID, "wrong"); err == nil {
		t.Fatal("passphrase bypass")
	}
	if _, _, err := h.caps.GallerySession(view.ID, "guest-secret"); err != nil {
		t.Fatal(err)
	}
	if err := h.caps.RevokeOwner(view.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if retry := request("POST", "hidden-confirmed", confirmed); !bytes.Equal(retry.Body.Bytes(), allowed.Body.Bytes()) {
		t.Fatal("revoked gallery reminted")
	}
}
