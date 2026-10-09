package library

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

func TestPhotoComponentCancellationSettlesIntentBeforeRestart(t *testing.T) {
	l, b := componentFixture(t)
	ctx := context.Background()
	f, err := storeComponent(l, ctx, "canceled-original", []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := l.photoComponentIntentPath(f.Path, f.Size, f.Hash)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := l.loadPhotoComponentIntent(p, f.Path, f.Size, f.Hash)
	if err != nil {
		t.Fatal(err)
	}
	intent.Canceled = true
	// Simulate a crash after the separate marker was persisted but before the
	// old intent and catalog row were removed. Even the catalog retry must fail.
	markerPath := photoComponentCancellationPath(p)
	if err := l.savePhotoComponentIntent(markerPath, intent); err != nil {
		t.Fatal(err)
	}
	if _, err := l.StorePhotoComponent(ctx, f.Path, failPhotoComponentReader{t}, f.Size, f.Hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("catalog retry bypassed cancellation: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("retry prematurely removed cleanup intent: %v", err)
	}
	commit := catalog.PhotoIngestCommit{DeviceID: "phone", DeviceAssetID: "asset", SourceRevision: "1", Files: []catalog.PhotoIngestFile{{ID: "original", From: f.Path, Size: f.Size, Hash: f.Hash}}}
	for i := 0; i < 2; i++ {
		if _, err := l.CancelPhotoIngest(ctx, commit, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		assertComponentIntentsSettled(t, l)
	}
	if _, ok := l.catalog.Get(f.Path); ok {
		t.Fatal("canceled row retained")
	}
	raw, err := os.ReadFile(markerPath)
	if err != nil || bytes.Contains(raw, []byte(f.Path)) {
		t.Fatalf("missing or plaintext cancellation marker: %v", err)
	}
	marker, err := l.loadPhotoComponentIntent(markerPath, f.Path, f.Size, f.Hash)
	if err != nil || !marker.Canceled || marker.File.Reference != nil {
		t.Fatalf("invalid settled marker: %+v %v", marker, err)
	}
	restarted := New(l.repo, filepath.Join(filepath.Dir(l.repo), "catalog.enc"), l.vault)
	restarted.backend = b
	restarted.photoAutoDisabled = true
	defer restarted.Drain(ctx)
	if _, err := restarted.StorePhotoComponent(ctx, f.Path, failPhotoComponentReader{t}, f.Size, f.Hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("restart recreated cancellation: %v", err)
	}
	assertComponentIntentsSettled(t, restarted)
	if _, err := restarted.CancelPhotoIngest(ctx, commit, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	assertComponentIntentsSettled(t, restarted)
}

func assertComponentIntentsSettled(t *testing.T, l *Library) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(l.repo), ".weazl-photo-components"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("component intents not settled: %v %v", entries, err)
	}
}
