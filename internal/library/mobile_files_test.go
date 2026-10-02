package library

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
)

func TestMobileFilesCapturedReadSurvivesOverwriteRenameAndPathReuse(t *testing.T) {
	for _, backend := range []string{"restic", "shared"} {
		t.Run(backend, func(t *testing.T) {
			l := newPhotoIndexTestLibrary(t)
			if backend == "restic" {
				if _, err := exec.LookPath("restic"); err != nil {
					t.Skip("restic not installed")
				}
				l.backend = newResticBackend(l.repo, l.vault)
			} else {
				store, err := sharedstore.Open(filepath.Join(t.TempDir(), "shared"), sharedstore.Options{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { store.Close() })
				l.ConfigureShared("owner", store, true)
			}
			ctx := context.Background()
			old, err := l.Put(ctx, "held.txt", []byte("old immutable content"))
			if err != nil {
				t.Fatal(err)
			}
			old, err = l.Metadata(ctx, "held.txt")
			if err != nil {
				t.Fatal(err)
			}
			err = l.ReadMobileFile(ctx, old.EntryID, func(item MobileFileItem, source PhotoRangeSource) error {
				if item.Revision != old.Revision || item.Path != old.Path {
					t.Fatalf("mixed metadata=%+v", item)
				}
				if _, err := l.Put(ctx, "held.txt", []byte("new")); err != nil {
					return err
				}
				if err := l.catalog.Rename("held.txt", "renamed.txt"); err != nil {
					return err
				}
				if _, err := l.Put(ctx, "held.txt", []byte("reused path")); err != nil {
					return err
				}
				var out bytes.Buffer
				if err := source(4, 9, &out); err != nil {
					return err
				}
				if out.String() != "immutable" {
					t.Fatalf("mixed bytes=%q", out.String())
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := l.MobileFileMetadata(ctx, old.EntryID)
			if err != nil || got.Path != "renamed.txt" || got.Revision <= old.Revision {
				t.Fatalf("stable lookup=%+v err=%v", got, err)
			}
			if err := l.Delete("renamed.txt"); err != nil {
				t.Fatal(err)
			}
			if err := l.ReadMobileFile(ctx, old.EntryID, func(MobileFileItem, PhotoRangeSource) error { t.Fatal("deleted file authorized"); return nil }); !errors.Is(err, catalog.ErrNotFound) {
				t.Fatalf("deleted read=%v", err)
			}
		})
	}
}

func TestMobileFilesLockStopsCapturedSource(t *testing.T) {
	l := newPhotoIndexTestLibrary(t)
	ctx := context.Background()
	if _, err := l.Put(ctx, "locked.txt", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	f, _ := l.catalog.Get("locked.txt")
	err := l.ReadMobileFile(ctx, f.EntryID, func(_ MobileFileItem, source PhotoRangeSource) error {
		l.vault.Lock()
		var out bytes.Buffer
		err := source(0, f.Size, &out)
		if out.Len() != 0 {
			t.Fatal("bytes emitted after lock")
		}
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("read after lock=%v", err)
	}
}
