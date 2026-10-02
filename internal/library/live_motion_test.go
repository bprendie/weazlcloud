package library

import (
	"bytes"
	"context"
	"encoding/hex"
	"strconv"
	"testing"
)

func TestLiveMotionReturnsOwnedBytesWithoutDestroyingRAMCache(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	a, _ := l.Put(ctx, "Photos/still.jpg", []byte("still"))
	b, _ := l.Put(ctx, "Photos/motion.mov", []byte("motion"))
	a, _ = l.catalog.Get(a.Path)
	b, _ = l.catalog.Get(b.Path)
	if _, err := l.LinkLivePhoto(ctx, a.EntryID, b.EntryID, a.Revision, b.Revision, false, false); err != nil {
		t.Fatal(err)
	}
	b, _ = l.catalog.Get(b.Path)
	fingerprint, err := l.vault.Fingerprint("live-motion-v1", []byte(a.EntryID+"\x00"+b.Hash+"\x00"+strconv.FormatUint(b.Revision, 10)))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(fingerprint)
	expected := []byte("immutable compatible motion")
	if err := l.writeThumbnailCache(hex.EncodeToString(fingerprint), thumbnailEnvelope{ContentType: "video/mp4", Body: expected}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		body, err := l.LiveMotion(ctx, a.EntryID, false)
		if err != nil || !bytes.Equal(body, expected) {
			t.Fatal("motion cache corrupted", body, err)
		}
		clear(body)
	}
}
