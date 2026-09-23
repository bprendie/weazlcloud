package library_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/desk"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

const baselinePass = "d0-disposable-vault-pass"

type baselineFixture struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func TestDedupeBaseline(t *testing.T) {
	if os.Getenv("WEAZLCLOUD_DEDUPE_BASELINE") != "1" {
		t.Skip("run through scripts/dedupe-baseline.sh on a disposable volume")
	}
	root := os.Getenv("WEAZLCLOUD_BASELINE_ROOT")
	fixtureRoot := os.Getenv("WEAZLCLOUD_DEDUPE_FIXTURES")
	if root == "" || fixtureRoot == "" {
		t.Fatal("baseline root and fixture root are required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fixtures := readBaselineFixtures(t, fixtureRoot)
	data := filepath.Join(root, "app")
	store, err := users.New(filepath.Join(data, "users.json"), filepath.Join(data, "users"))
	if err != nil {
		t.Fatal(err)
	}
	admin := createBaselineUser(t, store, "admin", true)
	alice := createBaselineUser(t, store, "alice", false)
	bob := createBaselineUser(t, store, "bob", false)
	q := quota.New(data)
	registry := filesvc.NewRegistry(store, q)
	handler := desk.NewMulti(store, capsule.New(filepath.Join(data, "capsules")), q, "", "", data, registry)
	server := httptest.NewServer(handler)
	defer server.Close()
	adminClient := baselineClient(t, server.URL, admin.Username)
	aliceClient := baselineClient(t, server.URL, alice.Username)
	bobClient := baselineClient(t, server.URL, bob.Username)
	fixturePaths := make([]string, 0, len(fixtures))
	for path := range fixtures {
		fixturePaths = append(fixturePaths, path)
	}
	sort.Strings(fixturePaths)
	for _, path := range fixturePaths {
		fixture := fixtures[path]
		fmt.Printf("D0_FIXTURE path=%q bytes=%d sha256=%s\n", path, fixture.Size, fixture.SHA256)
	}

	var writeDuration, readDuration time.Duration
	var logicalUploadBytes int64
	var memoryBefore, memoryAfter runtime.MemStats
	runtime.ReadMemStats(&memoryBefore)
	write := func(client *http.Client, role, label, appPath, fixture string) {
		t.Helper()
		d, size := uploadFixture(t, client, server.URL, role, appPath, filepath.Join(fixtureRoot, fixture))
		writeDuration += d
		logicalUploadBytes += size
	}

	write(aliceClient, "alice", "shared-copy", "shared/identical.bin", "shared/identical.bin")
	aliceAfterOneCopy, err := treeSpace(t, store.LibraryPath(alice))
	if err != nil {
		t.Fatal(err)
	}
	write(bobClient, "bob", "shared-copy", "shared/identical.bin", "shared/identical.bin")
	bobAfterOneCopy, err := treeSpace(t, store.LibraryPath(bob))
	if err != nil {
		t.Fatal(err)
	}
	write(aliceClient, "alice", "unique", "private/alice.bin", "unique/alice.bin")
	write(bobClient, "bob", "unique", "private/bob.bin", "unique/bob.bin")
	write(aliceClient, "alice", "repeated-chunks", "synthetic/repeated.bin", "repeated/repeated-chunks.bin")
	write(aliceClient, "alice", "disk-image-base", "images/disk-v1.img", "disk/disk-image-v1.img")
	write(aliceClient, "alice", "disk-image-modified-region", "images/disk-v2.img", "disk/disk-image-v2.img")
	write(aliceClient, "alice", "svg-preview", "previews/sample.svg", "previews/sample.svg")
	write(aliceClient, "alice", "markdown-preview", "previews/notes.md", "previews/notes.md")
	write(aliceClient, "alice", "empty-file", "empty/zero.bin", "empty/zero.bin")
	baselineCreateFolder(t, aliceClient, server.URL, "empty-folder")
	baselineSamePathTrash(t, aliceClient, server.URL, fixtureRoot, &writeDuration, &logicalUploadBytes)
	baselineBatch(t, aliceClient, server.URL, fixtureRoot, &writeDuration, &logicalUploadBytes)

	for _, check := range []struct {
		client *http.Client
		role   string
		path   string
		file   string
	}{
		{aliceClient, "alice", "shared/identical.bin", "shared/identical.bin"},
		{bobClient, "bob", "shared/identical.bin", "shared/identical.bin"},
		{aliceClient, "alice", "private/alice.bin", "unique/alice.bin"},
		{bobClient, "bob", "private/bob.bin", "unique/bob.bin"},
		{aliceClient, "alice", "synthetic/repeated.bin", "repeated/repeated-chunks.bin"},
		{aliceClient, "alice", "images/disk-v2.img", "disk/disk-image-v2.img"},
		{aliceClient, "alice", "empty/zero.bin", "empty/zero.bin"},
		{aliceClient, "alice", "versions/same-path.txt", "versions/same-path-v2.txt"},
		{aliceClient, "alice", "batch/one.txt", "batch/one.txt"},
		{aliceClient, "alice", "batch/two.txt", "batch/two.txt"},
	} {
		readDuration += verifyDownload(t, check.client, server.URL, check.role, check.path, fixtures[check.file])
	}
	verifyPreview(t, aliceClient, server.URL, "previews/sample.svg", "<svg")
	verifyPreview(t, aliceClient, server.URL, "previews/notes.md", "D0 preview fixture")
	verifyAdminIsolation(t, adminClient, server.URL)
	verifyBatchCatalog(t, store, alice, fixtures)

	aliceSpace, err := treeSpace(t, store.LibraryPath(alice))
	if err != nil {
		t.Fatal(err)
	}
	bobSpace, err := treeSpace(t, store.LibraryPath(bob))
	if err != nil {
		t.Fatal(err)
	}
	dataSpace, err := treeSpace(t, data)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&memoryAfter)
	goHWM := processHighWaterKiB()

	if _, ok := store.User(admin.ID); !ok {
		t.Fatal("baseline administrator disappeared")
	}
	fmt.Printf("D0_BASELINE accounts=2_ordinary+1_admin restic=%s\n", resticVersion())
	fmt.Printf("D0_BASELINE alice_repo_allocated=%d alice_repo_apparent=%d bob_repo_allocated=%d bob_repo_apparent=%d\n", aliceSpace.allocated, aliceSpace.apparent, bobSpace.allocated, bobSpace.apparent)
	fmt.Printf("D0_BASELINE total_app_allocated=%d total_app_apparent=%d alice_repo_after_one_copy=%d bob_repo_after_one_copy=%d\n", dataSpace.allocated, dataSpace.apparent, aliceAfterOneCopy.allocated, bobAfterOneCopy.allocated)
	fmt.Printf("D0_BASELINE logical_upload_bytes=%d write_request_time_sum_ms=%d verified_read_time_sum_ms=%d go_alloc_delta=%d go_heap_after=%d go_test_hwm_kib=%d\n", logicalUploadBytes, writeDuration.Milliseconds(), readDuration.Milliseconds(), memoryAfter.TotalAlloc-memoryBefore.TotalAlloc, memoryAfter.HeapAlloc, goHWM)
	fmt.Printf("D0_BASELINE interpretation=per-user_restic_repositories_do_not_share_cross-user_objects_fixture_set=synthetic_not_a_household_savings_forecast\n")
}

func createBaselineUser(t *testing.T, store *users.Store, username string, admin bool) users.User {
	t.Helper()
	u, err := store.Create(username, "d0-disposable-account-pass", admin)
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(store.VaultPath(u), store.NodeKeyPath(u))
	if err := v.Forge([]byte(baselinePass), []byte(baselinePass)); err != nil {
		t.Fatal(err)
	}
	return u
}

func baselineClient(t *testing.T, base, username string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	login := postJSON(t, client, base, "/api/login", map[string]string{"username": username, "password": "d0-disposable-account-pass"}, http.StatusOK)
	login.Body.Close()
	unlock := postJSON(t, client, base, "/api/unlock", map[string]string{"passphrase": baselinePass}, http.StatusOK)
	unlock.Body.Close()
	return client
}

func readBaselineFixtures(t *testing.T, root string) map[string]baselineFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Format int               `json:"format"`
		Files  []baselineFixture `json:"files"`
	}
	if err := json.Unmarshal(b, &manifest); err != nil || manifest.Format != 1 {
		t.Fatalf("invalid fixture manifest: %v", err)
	}
	out := make(map[string]baselineFixture, len(manifest.Files))
	for _, fixture := range manifest.Files {
		out[fixture.Path] = fixture
		if got := hashFile(t, filepath.Join(root, fixture.Path)); got != fixture.SHA256 {
			t.Fatalf("fixture hash mismatch for %q", fixture.Path)
		}
	}
	return out
}

func postJSON(t *testing.T, client *http.Client, base, path string, body any, want int) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Weazl-Desk", "1")
	return baselineDo(t, client, req, want)
}

func baselineDo(t *testing.T, client *http.Client, req *http.Request, want int) *http.Response {
	t.Helper()
	req.Header.Set("X-Weazl-Desk", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		t.Fatalf("%s %s returned %d, want %d: %s", req.Method, req.URL.Path, resp.StatusCode, want, body)
	}
	return resp
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
