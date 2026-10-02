package capsule

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func mobileFixture(t *testing.T) (*Store, *vault.Vault) {
	t.Helper()
	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"), filepath.Join(dir, "node.key"))
	if err := v.Forge([]byte("test"), []byte("test")); err != nil {
		t.Fatal(err)
	}
	return New(filepath.Join(dir, "caps")), v
}
func mobileTestMint(t *testing.T, owner string, count *atomic.Int32) func(*Store) MobileResult {
	return func(s *Store) MobileResult {
		count.Add(1)
		_, err := s.MintStream(Record{Owner: owner, Name: "secret-file", Gate: "open", Limit: 2, Expires: time.Now().Add(time.Hour)}, "", func(w io.Writer) error { _, e := io.WriteString(w, "secret bytes"); return e })
		if err != nil {
			t.Fatal(err)
		}
		return MobileResult{Status: 200}
	}
}
func TestMobileMintConcurrentRestartConflictAndBurn(t *testing.T) {
	s, v := mobileFixture(t)
	var count atomic.Int32
	mint := mobileTestMint(t, "owner", &count)
	var wg sync.WaitGroup
	results := make(chan MobileResult, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := New(s.root).MobileMint(v, "owner", "private-key", []byte("canonical body"), "https://grab.test", 200, nil, mint)
			if err != nil {
				t.Error(err)
			}
			results <- got
		}()
	}
	wg.Wait()
	close(results)
	var original MobileResult
	for got := range results {
		if original.Body == nil {
			original = got
		}
		if !bytes.Equal(got.Body, original.Body) {
			t.Errorf("different results")
		}
	}
	if count.Load() != 1 || len(s.ListOwner("owner")) != 1 {
		t.Fatal("duplicate admission")
	}
	s = New(s.root)
	got, err := s.MobileMint(v, "owner", "private-key", []byte("canonical body"), "https://changed.test", 200, nil, mint)
	if err != nil || !bytes.Equal(got.Body, original.Body) {
		t.Fatalf("restart: %v", err)
	}
	_, err = s.MobileMint(v, "owner", "private-key", []byte("different"), "", 200, nil, mint)
	if !errors.Is(err, ErrMobileConflict) {
		t.Fatalf("conflict=%v", err)
	}
	rec := s.ListOwner("owner")[0]
	if rec.Used != 0 {
		t.Fatal("retry consumed grab")
	}
	if err := s.RevokeOwner(rec.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	got, err = s.MobileLookup(v, "owner", "private-key")
	if err != nil || !bytes.Equal(got.Body, original.Body) || count.Load() != 1 {
		t.Fatal("revoked operation reminted")
	}
	other, err := s.MobileLookup(v, "other", "private-key")
	if err != nil || other.Status != 404 {
		t.Fatal("foreign operation visible")
	}
	dir := filepath.Join(filepath.Dir(v.Path()), ".mobile-grabs")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if bytes.Contains(raw, []byte("private-key")) || bytes.Contains(raw, []byte("secret-file")) {
			t.Fatal("plaintext private metadata")
		}
	}
}

func TestMobileCrashAfterPublicationBeforeReply(t *testing.T) {
	s, v := mobileFixture(t)
	var count atomic.Int32
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected simulated crash")
			}
		}()
		_, _ = s.MobileMint(v, "owner", "crash", []byte("spec"), "https://grab.test", 201, nil, func(scoped *Store) MobileResult {
			mobileTestMint(t, "owner", &count)(scoped)
			panic("after actual mint")
		})
	}()
	got, err := New(s.root).MobileMint(v, "owner", "crash", []byte("spec"), "https://other.test", 201, nil, mobileTestMint(t, "owner", &count))
	if err != nil || got.Status != 201 || count.Load() != 1 || !bytes.Contains(got.Body, []byte("https://grab.test/g/")) {
		t.Fatalf("recovery=%s err=%v calls=%d", got.Body, err, count.Load())
	}
}

func TestMobileInterruptedOperationNeverReadmits(t *testing.T) {
	s, v := mobileFixture(t)
	func() {
		defer func() { _ = recover() }()
		_, _ = s.MobileMint(v, "owner", "cancel", []byte("spec"), "https://grab.test", 200, nil, func(*Store) MobileResult { panic("before source capture") })
	}()
	var count atomic.Int32
	got, err := New(s.root).MobileMint(v, "owner", "cancel", []byte("spec"), "https://grab.test", 200, nil, mobileTestMint(t, "owner", &count))
	if err != nil || got.Status != 409 || count.Load() != 0 || len(s.List()) != 0 {
		t.Fatalf("interrupted operation was admitted: %v", err)
	}
	v.Lock()
	_, err = s.MobileLookup(v, "owner", "cancel")
	if !errors.Is(err, vault.ErrLocked) {
		t.Fatal("locked operation accessible")
	}
}

func TestMobileLookupWhileMinting(t *testing.T) {
	s, v := mobileFixture(t)
	var count atomic.Int32
	result, err := s.MobileMint(v, "owner", "status", []byte("spec"), "https://grab.test", 200, nil, func(scoped *Store) MobileResult {
		status, err := New(s.root).MobileLookup(v, "owner", "status")
		if err != nil || status.Status != 202 || !bytes.Contains(status.Body, []byte(`"state":"minting"`)) {
			t.Fatalf("running status=%s %v", status.Body, err)
		}
		return mobileTestMint(t, "owner", &count)(scoped)
	})
	if err != nil || result.Status != 200 || count.Load() != 1 {
		t.Fatal("mint failed")
	}
}

func TestMobileGalleryCrashAfterPublication(t *testing.T) {
	s, v := mobileFixture(t)
	calls := 0
	mint := func(scoped *Store) MobileResult {
		calls++
		rec, err := scoped.MintGallery(Record{Owner: "owner", Label: "Gallery", Gate: "open", Limit: 2, Expires: time.Now().Add(time.Hour)}, "", []GallerySource{{Item: GalleryItem{Name: "a.jpg", Size: 3, Revision: 7}, Original: func(w io.Writer) error { _, e := io.WriteString(w, "abc"); return e }}})
		if err != nil {
			t.Fatal(err)
		}
		if rec.ID == "" {
			t.Fatal("missing ID")
		}
		panic("after gallery publication")
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = s.MobileMint(v, "owner", "gallery-crash", []byte("spec"), "https://grab.test", 201, nil, mint)
	}()
	got, err := New(s.root).MobileMint(v, "owner", "gallery-crash", []byte("spec"), "https://grab.test", 201, nil, func(*Store) MobileResult { t.Fatal("gallery admitted again"); return MobileResult{} })
	if err != nil || got.Status != 201 || calls != 1 || len(s.ListOwner("owner")) != 1 {
		t.Fatal("gallery not recovered")
	}
}
