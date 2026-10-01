package desk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/library"
)

func smokeCorruptPhotoGallery(t *testing.T, h *Handler, post func(string, any) *http.Response) {
	t.Helper()
	l := h.registry.For(h.users.Users()[0]).Lib
	original := []byte("damaged photo bytes remain recoverable")
	if _, err := l.Put(context.Background(), "Photos/damaged.jpg", original); err != nil {
		t.Fatal(err)
	}
	page, err := l.PhotoSearchPage(context.Background(), library.PhotoSearchOptions{Query: "damaged.jpg", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("damaged asset lookup=%+v %v", page, err)
	}
	res := post("/api/v1/photos/grabs", map[string]any{"ids": []string{page.Items[0].ID}, "title": "Damaged original", "gate": "open", "grabs": 2})
	var grab struct{ ID string }
	err = json.NewDecoder(res.Body).Decode(&grab)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusCreated || grab.ID == "" {
		t.Fatalf("bad derivative aborted gallery: status=%d %v", res.StatusCode, err)
	}
	manifest, token, err := h.caps.GallerySession(grab.ID, "")
	if err != nil || len(manifest.Items) != 1 || manifest.Items[0].PreviewType != "" {
		t.Fatalf("missing derivative placeholder=%+v %v", manifest, err)
	}
	var frozen bytes.Buffer
	_, err = h.caps.GalleryOriginal(grab.ID, capsule.GalleryAuth{Session: token}, manifest.Items[0].ID, func(capsule.Record, capsule.GalleryItem) (io.Writer, error) { return &frozen, nil })
	if err != nil || !bytes.Equal(frozen.Bytes(), original) {
		t.Fatalf("damaged original did not remain downloadable: %v", err)
	}
}
