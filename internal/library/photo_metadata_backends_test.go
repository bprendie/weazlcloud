package library

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/sharedstore"
)

func TestMetadataPendingRestartOnResticAndSharedStorage(t *testing.T) {
	for _, kind := range []string{"restic", "shared"} {
		t.Run(kind, func(t *testing.T) {
			l := newPhotoIndexTestLibrary(t)
			var store *sharedstore.Store
			if kind == "restic" {
				if _, err := exec.LookPath("restic"); err != nil {
					t.Skip("restic not installed")
				}
				l.backend = newResticBackend(l.repo, l.vault)
			} else {
				var err error
				store, err = sharedstore.Open(filepath.Join(t.TempDir(), "shared"), sharedstore.Options{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				l.ConfigureShared("metadata-owner", store, true)
			}
			ctx := context.Background()
			if _, err := l.Put(ctx, "Photos/a.png", []byte("original")); err != nil {
				t.Fatal(err)
			}
			if _, err := l.Put(ctx, "Photos/a.png.json", []byte(`{"photoTakenTime":{"timestamp":"1365152400"}}`)); err != nil {
				t.Fatal(err)
			}
			// Occupy background admission so the checkpoint is deterministically
			// paused before any source read, without a production delay setting.
			held := 0
			for len(thumbnailBackfill) < cap(thumbnailBackfill) {
				thumbnailBackfill <- struct{}{}
				held++
			}
			release := func() {
				for held > 0 {
					<-thumbnailBackfill
					held--
				}
			}
			defer release()
			if _, err := l.SetPhotoMetadataJob(ctx, "start", PhotoMetadataOptionsJob{SidecarsOnly: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := l.SetPhotoMetadataJob(ctx, "pause", PhotoMetadataOptionsJob{}); err != nil {
				t.Fatal(err)
			}
			drainCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := l.Drain(drainCtx); err != nil {
				t.Fatal(err)
			}
			release()
			restarted := New(l.repo, filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
			restarted.backend = l.backend
			restarted.photoAutoDisabled = true
			if store != nil {
				restarted.ConfigureShared("metadata-owner", store, true)
			}
			t.Cleanup(func() { stopPhotoIndexSaveForTest(restarted) })
			state, err := restarted.PhotoMetadataStatus()
			if err != nil || state.Status != "paused" || state.Examined != 0 {
				t.Fatal("lost pending checkpoint", state, err)
			}
			if _, err = restarted.SetPhotoMetadataJob(ctx, "resume", PhotoMetadataOptionsJob{}); err != nil {
				t.Fatal(err)
			}
			state = waitMetadata(t, restarted)
			if state.Updated != 1 || state.Failed != 0 {
				t.Fatal(state)
			}
			body, err := restarted.Get(ctx, "Photos/a.png")
			if err != nil || string(body) != "original" {
				t.Fatal("original changed", err)
			}
		})
	}
}
