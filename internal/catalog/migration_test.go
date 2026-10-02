package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func migrationFixture(t *testing.T, c *Catalog) (tree, []byte, map[string][]byte) {
	t.Helper()
	when := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	offset := -240
	payload := []byte("fixture original bytes")
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	files := []File{
		{EntryID: "folder", Revision: 5, Path: "Photos/Private", Folder: true, Hidden: true, Present: true, Mtime: when},
		{EntryID: "still", Revision: 8, Path: "Photos/Private/a.jpg", Size: int64(len(payload)), Hash: hash, Snap: "snapshot-a", Present: true, Hidden: true, Mtime: when, ImportedAt: when, CaptureTime: &when, CaptureOffsetMinutes: &offset, CaptureSource: "client", CaptureUserCorrected: true, Favorite: true, DeviceID: "phone", DeviceAssetID: "asset", SourceRevision: "1", PhotoComponents: []PhotoComponent{{ID: "original", AssetID: "still", MediaType: "image/jpeg"}, {ID: "motion", AssetID: "motion", MediaType: "video/quicktime"}}},
		{EntryID: "motion", Revision: 3, Path: "Photos/Private/a.mov", Size: int64(len(payload)), Hash: hash, Snap: "snapshot-a", Present: true, Hidden: true, Mtime: when, ImportedAt: when, PhotoParentID: "still"},
		{EntryID: "unknown", Revision: 2, Path: "Photos/b.jpg", Size: int64(len(payload)), Hash: hash, Snap: "snapshot-a", Present: true, Mtime: when, ImportedAt: when},
	}
	for i := range files {
		if err := assignReference(&files[i]); err != nil {
			t.Fatal(err)
		}
	}
	legacy := tree{Schema: 1, Files: files, Albums: []Album{{ID: "pa_legacy", Revision: 9, Title: "Legacy", AssetIDs: []string{"still", "unknown"}, CoverID: "still"}}, Journal: Journal{SyncPosition: SyncPosition{Epoch: "retained-epoch"}}, Checkpoints: map[string]SyncPosition{"phone": {Epoch: "retained-epoch"}}}
	plain, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.vault.Wrap(plain)
	clear(plain)
	if err != nil {
		t.Fatal(err)
	}
	storage := map[string][]byte{}
	for _, name := range []string{"original.enc", "legacy-receipt.enc"} {
		b, err := c.vault.Wrap(payload)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(filepath.Dir(c.path), "fixture-media", name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		storage[path] = b
	}
	return legacy, raw, storage
}
func TestCatalogMigrationRecoveryRepeatAndOldWriterRefusal(t *testing.T) {
	c := testCatalog(t)
	legacy, raw, storage := migrationFixture(t, c)
	var physical int
	for _, b := range storage {
		physical += len(b)
	}
	for run := 0; run < 2; run++ {
		if err := os.WriteFile(c.path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		before := New(c.path, c.vault)
		if err := before.LoadReadOnly(); err != nil {
			t.Fatal(err)
		}
		l, u, tr, n := before.Summary()
		upgraded := New(c.path, c.vault)
		if err := upgraded.Load(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(upgraded.All(), legacy.Files) || !reflect.DeepEqual(upgraded.Albums(), legacy.Albums) {
			t.Fatal("IDs/revisions/metadata/membership/reference preservation failed")
		}
		if !reflect.DeepEqual(upgraded.checkpoints, legacy.Checkpoints) {
			t.Fatal("device checkpoint lost")
		}
		al, au, at, an := upgraded.Summary()
		if [4]int64{l, u, tr, int64(n)} != [4]int64{al, au, at, int64(an)} {
			t.Fatal("accounting changed")
		}
		recovery, err := os.ReadFile(upgraded.RecoveryPath())
		if err != nil || !bytes.Equal(recovery, raw) {
			t.Fatalf("recovery differs: %v", err)
		}
		info, err := os.Stat(upgraded.RecoveryPath())
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("recovery permissions")
		}
		if bytes.Contains(recovery, []byte("fixture original bytes")) || bytes.Contains(recovery, []byte("pa_legacy")) {
			t.Fatal("plaintext recovery")
		}
		current, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := c.vault.Unwrap(current)
		if err != nil {
			t.Fatal(err)
		}
		var old struct {
			Files []File `json:"files"`
		}
		if json.Unmarshal(plain, &old) == nil {
			t.Fatal("old writer can erase schema 2")
		}
		clear(plain)
		if err = upgraded.Load(); err != nil {
			t.Fatal(err)
		}
		again, _ := os.ReadFile(c.path)
		if !bytes.Equal(current, again) {
			t.Fatal("second load rewrote current schema")
		}
		total := 0
		for path, original := range storage {
			b, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(b, original) {
				t.Fatal("media/receipt modified")
			}
			total += len(b)
		}
		if total != physical {
			t.Fatal("physical encrypted media bytes changed")
		}
	}
}
func TestCatalogMigrationFutureSchemaAndRecoveryFailures(t *testing.T) {
	c := testCatalog(t)
	_, raw, _ := migrationFixture(t, c)
	for _, schema := range []int{-1, 3, 999} {
		plain, _ := json.Marshal(map[string]any{"schema": schema, "files": map[string]any{"entries": []File{}}})
		sealed, err := c.vault.Wrap(plain)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(c.path, sealed, 0600); err != nil {
			t.Fatal(err)
		}
		for _, load := range []func() error{c.Load, c.LoadReadOnly} {
			if err = load(); !errors.Is(err, ErrCollectionSchema) {
				t.Fatalf("schema %d: %v", schema, err)
			}
			after, _ := os.ReadFile(c.path)
			if !bytes.Equal(after, sealed) {
				t.Fatal("future catalog rewritten")
			}
		}
	}
	if _, err := os.Stat(c.RecoveryPath()); !os.IsNotExist(err) {
		t.Fatal("recovery created for rejected schema")
	}
	if err := os.WriteFile(c.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(c.RecoveryPath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.Load(); err == nil {
		t.Fatal("migration proceeded without recovery")
	}
	after, _ := os.ReadFile(c.path)
	if !bytes.Equal(after, raw) {
		t.Fatal("failed migration replaced catalog")
	}
	if err := os.Remove(c.RecoveryPath()); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash boundary after recovery fsync, before catalog replacement.
	if err := c.preserveMigrationRecovery(raw); err != nil {
		t.Fatal(err)
	}
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	// A different restored legacy catalog must never overwrite the existing copy.
	different, _ := c.vault.Wrap([]byte(`{"files":[],"schema":1}`))
	if err := os.WriteFile(c.path, different, 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Load(); !errors.Is(err, ErrMigrationRecoveryConflict) {
		t.Fatalf("recovery conflict: %v", err)
	}
	after, _ = os.ReadFile(c.path)
	if !bytes.Equal(after, different) {
		t.Fatal("conflict replaced catalog")
	}
}
