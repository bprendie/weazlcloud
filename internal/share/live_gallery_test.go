package share

import (
	"bytes"
	"encoding/json"
	"github.com/bprendie/weazlcloud/internal/capsule"
	"io"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLiveGalleryMotionHTTPAuthorizationTypeAndRetry(t *testing.T) {
	store := capsule.New(t.TempDir())
	original := func(w io.Writer) error { _, err := w.Write([]byte("source")); return err }
	sources := []capsule.GallerySource{
		{SourceID: "owner-still", Item: capsule.GalleryItem{Name: "still.jpg", Size: 6}, Original: original},
		{SourceID: "owner-motion", ParentID: "owner-still", Item: capsule.GalleryItem{Name: "motion.mov", Size: 6}, Original: original, MotionPreview: func() ([]byte, error) { return []byte("compatible motion"), nil }},
	}
	rec, err := store.MintGallery(capsule.Record{Gate: "open", Limit: 2, Expires: time.Now().Add(time.Hour)}, "", sources)
	if err != nil {
		t.Fatal(err)
	}
	manifest, session, err := store.RenewGallerySession(rec.ID, capsule.GalleryAuth{})
	if err != nil {
		t.Fatal(err)
	}
	h := New(store)
	request := func(token string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"session": token})
		r := httptest.NewRequest("POST", "/g/"+rec.ID+"/motion/"+manifest.Items[0].MotionID, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request(""); w.Code == 200 {
		t.Fatal("unauthorized motion")
	}
	w := request(session)
	if w.Code != 200 || w.Header().Get("Content-Type") != "video/mp4" || w.Body.String() != "compatible motion" {
		t.Fatalf("motion: %d %s %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	current, err := store.Meta(rec.ID)
	if err != nil || current.Used != 0 {
		t.Fatal("motion spent retry", current, err)
	}
	if err := store.Revoke(rec.ID); err != nil {
		t.Fatal(err)
	}
	if w := request(session); w.Code == 200 {
		t.Fatal("revoked motion exposed")
	}
}
