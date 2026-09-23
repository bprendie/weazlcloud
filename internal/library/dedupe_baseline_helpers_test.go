package library_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type fileSpace struct{ allocated, apparent int64 }

func uploadFixture(t *testing.T, client *http.Client, base, role, appPath, fixturePath string) (time.Duration, int64) {
	t.Helper()
	file, err := os.Open(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, base+"/api/library?path="+url.QueryEscape(appPath), file)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = info.Size()
	started := time.Now()
	resp := baselineDo(t, client, req, http.StatusOK)
	resp.Body.Close()
	return time.Since(started), info.Size()
}

func verifyDownload(t *testing.T, client *http.Client, base, role, appPath string, fixture baselineFixture) time.Duration {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/library?path="+url.QueryEscape(appPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	resp := baselineDo(t, client, req, http.StatusOK)
	h := sha256.New()
	n, copyErr := io.Copy(h, resp.Body)
	resp.Body.Close()
	if copyErr != nil || n != fixture.Size || hex.EncodeToString(h.Sum(nil)) != fixture.SHA256 {
		t.Fatalf("download verification failed for role=%s fixture=%s: bytes=%d err=%v", role, fixture.Path, n, copyErr)
	}
	return time.Since(started)
}

func baselineCreateFolder(t *testing.T, client *http.Client, base, path string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"path": path})
	req, err := http.NewRequest(http.MethodPost, base+"/api/library/folder", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp := baselineDo(t, client, req, http.StatusCreated)
	resp.Body.Close()
}

func baselineSamePathTrash(t *testing.T, client *http.Client, base, fixtureRoot string, elapsed *time.Duration, bytes *int64) {
	t.Helper()
	duration, size := uploadFixture(t, client, base, "alice", "versions/same-path.txt", filepath.Join(fixtureRoot, "versions/same-path-v1.txt"))
	*elapsed += duration
	*bytes += size
	req, err := http.NewRequest(http.MethodDelete, base+"/api/library?path="+url.QueryEscape("versions/same-path.txt"), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp := baselineDo(t, client, req, http.StatusOK)
	resp.Body.Close()
	duration, size = uploadFixture(t, client, base, "alice", "versions/same-path.txt", filepath.Join(fixtureRoot, "versions/same-path-v2.txt"))
	*elapsed += duration
	*bytes += size
	req, err = http.NewRequest(http.MethodGet, base+"/api/trash", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp = baselineDo(t, client, req, http.StatusOK)
	trash, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil || !strings.Contains(string(trash), "versions/same-path.txt") {
		t.Fatalf("same-path trashed record missing: %v", readErr)
	}
}

func baselineBatch(t *testing.T, client *http.Client, base, fixtureRoot string, elapsed *time.Duration, bytes *int64) {
	t.Helper()
	type result struct {
		duration time.Duration
		size     int64
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, item := range []struct{ appPath, fixture string }{
		{"batch/one.txt", "batch/one.txt"},
		{"batch/two.txt", "batch/two.txt"},
	} {
		item := item
		go func() {
			<-start
			file, err := os.Open(filepath.Join(fixtureRoot, item.fixture))
			if err != nil {
				results <- result{err: err}
				return
			}
			info, err := file.Stat()
			if err != nil {
				file.Close()
				results <- result{err: err}
				return
			}
			req, err := http.NewRequest(http.MethodPut, base+"/api/library?path="+url.QueryEscape(item.appPath), file)
			if err != nil {
				file.Close()
				results <- result{err: err}
				return
			}
			req.ContentLength = info.Size()
			req.Header.Set("X-Weazl-Desk", "1")
			began := time.Now()
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					err = fmt.Errorf("batch upload returned %d", resp.StatusCode)
				}
			}
			file.Close()
			results <- result{duration: time.Since(began), size: info.Size(), err: err}
		}()
	}
	close(start)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		*elapsed += result.duration
		*bytes += result.size
	}
}

func verifyPreview(t *testing.T, client *http.Client, base, path, want string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/library?path="+url.QueryEscape(path)+"&preview=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp := baselineDo(t, client, req, http.StatusOK)
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if readErr != nil || !strings.Contains(string(b), want) {
		t.Fatalf("preview failed for fixture=%q err=%v", path, readErr)
	}
}

func verifyAdminIsolation(t *testing.T, client *http.Client, base string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/library?path="+url.QueryEscape("shared/identical.bin"), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("admin unexpectedly read an ordinary user's private file")
	}
}

func verifyBatchCatalog(t *testing.T, store *users.Store, alice users.User, fixtures map[string]baselineFixture) {
	t.Helper()
	v := vault.New(store.VaultPath(alice), store.NodeKeyPath(alice))
	if err := v.Unlock([]byte(baselinePass)); err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile(store.CatalogPath(alice))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := v.Unwrap(sealed)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	var tree struct {
		Files []catalog.File `json:"files"`
	}
	if err := json.Unmarshal(plain, &tree); err != nil {
		t.Fatal(err)
	}
	snapshots := make(map[string]string)
	trashedV1, liveV2 := 0, 0
	for _, file := range tree.Files {
		if file.Present && (file.Path == "batch/one.txt" || file.Path == "batch/two.txt") {
			snapshots[file.Path] = file.Snap
		}
		if file.Path == "versions/same-path.txt" && !file.Present && file.DeletedAt != nil && file.Hash == fixtures["versions/same-path-v1.txt"].SHA256 {
			trashedV1++
		}
		if file.Path == "versions/same-path.txt" && file.Present && file.Hash == fixtures["versions/same-path-v2.txt"].SHA256 {
			liveV2++
		}
	}
	if snapshots["batch/one.txt"] == "" || snapshots["batch/one.txt"] != snapshots["batch/two.txt"] {
		t.Fatalf("batch fixtures do not share one Restic snapshot")
	}
	if trashedV1 != 1 || liveV2 != 1 {
		t.Fatalf("same-path versions not preserved independently: trashed=%d live=%d", trashedV1, liveV2)
	}
}

func treeSpace(t *testing.T, root string) (fileSpace, error) {
	t.Helper()
	var total fileSpace
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total.apparent += info.Size()
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			total.allocated += stat.Blocks * 512
		}
		return nil
	})
	return total, err
}

func processHighWaterKiB() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			fields := strings.Fields(line)
			value, _ := strconv.ParseInt(fields[1], 10, 64)
			return value
		}
	}
	return 0
}

func resticVersion() string {
	b, err := exec.Command("restic", "version").Output()
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(b))
}
