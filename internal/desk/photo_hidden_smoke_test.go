package desk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

func smokePhotoHiddenOwnerIsolation(t *testing.T, h *Handler, client *http.Client, base string, post func(string, any) *http.Response, assetID, grabID, token, guestID string) {
	t.Helper()
	checkPost := func(path string, body any, status int) {
		t.Helper()
		res := post(path, body)
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("%s=%d want=%d", path, res.StatusCode, status)
		}
	}
	checkPost("/api/unlock", map[string]string{"passphrase": "photo-vault"}, 200)
	l := h.registry.For(h.users.Users()[0]).Lib
	if _, err := l.SetPhotoFolderHidden(context.Background(), "Photos", true); err != nil {
		t.Fatal(err)
	}
	album, err := l.SavePhotoAlbum(context.Background(), catalog.Album{Title: "Private album", AssetIDs: []string{assetID}})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := l.CreatePhotoSelection(context.Background(), library.PhotoSelectionOptions{IDs: []string{assetID}, Filter: library.PhotoSearchOptions{Hidden: true}})
	if err != nil {
		t.Fatal(err)
	}
	checkGet := func(path string, status int) {
		t.Helper()
		res, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("%s=%d want=%d", path, res.StatusCode, status)
		}
	}
	checkGet("/api/v1/photos/assets/"+assetID+"/original", 404)
	checkGet("/api/v1/photos/assets/"+assetID+"/original?hidden=1", 200)
	for _, view := range []struct {
		mode  string
		count int
	}{{"all", 0}, {"hidden", 2}} {
		res, err := client.Get(base + "/api/v1/photos?mode=" + view.mode)
		if err != nil {
			t.Fatal(err)
		}
		var page library.PhotoPage
		err = json.NewDecoder(res.Body).Decode(&page)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || len(page.Items) != view.count {
			t.Fatalf("owner %s timeline=%+v %v", view.mode, page, err)
		}
		for _, endpoint := range []string{"dates", "seek"} {
			res, err = client.Get(base + "/api/v1/photos/" + endpoint + "?mode=" + view.mode)
			if err != nil {
				t.Fatal(err)
			}
			var scoped struct {
				Total int `json:"total"`
			}
			err = json.NewDecoder(res.Body).Decode(&scoped)
			res.Body.Close()
			if err != nil || res.StatusCode != 200 || scoped.Total != view.count {
				t.Fatalf("%s %s=%+v %v", endpoint, view.mode, scoped, err)
			}
		}
	}
	// Hiding a source is presentation only; the existing frozen grab survives.
	var preview bytes.Buffer
	if _, err := h.caps.GalleryPreview(grabID, capsule.GalleryAuth{Session: token}, guestID, &preview); err != nil || preview.Len() == 0 {
		t.Fatalf("hiding unexpectedly revoked frozen grant: %v", err)
	}
	checkPost("/api/users", map[string]string{"username": "other", "password": "other-password", "vault_passphrase": "other-vault"}, 201)
	checkPost("/api/logout", map[string]any{}, 200)
	checkPost("/api/login", map[string]string{"username": "other", "password": "other-password"}, 200)
	checkPost("/api/unlock", map[string]string{"passphrase": "other-vault"}, 200)
	checkGet("/api/v1/photos/assets/"+assetID+"/original?hidden=1&owner=photo", 404)
	checkGet("/api/v1/photos/albums/memberships?id="+album.ID+"&hidden=1&owner=photo", 404)
	res, err := client.Get(base + "/api/v1/photos?mode=hidden&owner=photo")
	if err != nil {
		t.Fatal(err)
	}
	var page library.PhotoPage
	err = json.NewDecoder(res.Body).Decode(&page)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 || len(page.Items) != 0 {
		t.Fatalf("cross-owner hidden timeline=%+v %v", page, err)
	}
	for _, endpoint := range []string{"dates", "seek", "metadata-jobs"} {
		res, err = client.Get(base + "/api/v1/photos/" + endpoint + "?mode=hidden&owner=photo")
		if err != nil {
			t.Fatal(err)
		}
		var scoped struct {
			Total int `json:"total"`
		}
		err = json.NewDecoder(res.Body).Decode(&scoped)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || scoped.Total != 0 {
			t.Fatalf("cross-owner %s=%+v %v", endpoint, scoped, err)
		}
	}
	checkGet("/api/v1/photos/metadata-jobs?report=1&cursor=-1", 400)
	checkGet("/api/v1/photos/seek?around="+assetID+"&mode=hidden&owner=photo", 409)
	res = post("/api/v1/photos/selection-actions", map[string]any{"selection_id": selection.ID, "action": "archive", "hidden": true, "confirm_hidden": true})
	res.Body.Close()
	if res.StatusCode < 400 {
		t.Fatal("another owner applied a private hidden selection")
	}
}
