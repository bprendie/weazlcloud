package filesvc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupExpiredArchivesHonorsRetentionAndReportsBytes(t *testing.T) {
	root := t.TempDir()
	m := &ArchiveManager{root: root, jobs: make(map[string]*archiveJob)}
	oldPath := filepath.Join(root, "expired.zip")
	recentPath := filepath.Join(root, "recent.zip")
	tempPath := filepath.Join(root, ".archive-abandoned.zip")
	for path, body := range map[string]string{oldPath: "expired-data", recentPath: "keep", tempPath: "temp-data"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-archiveLifetime - time.Minute)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tempPath, old, old); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := m.CleanupExpired(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed != int64(len("expired-data")+len("temp-data")) {
		t.Fatalf("reclaimed=%d", reclaimed)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expired archive remains: %v", err)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("abandoned temp remains: %v", err)
	}
	if _, err := os.Stat(recentPath); err != nil {
		t.Fatalf("recent archive removed: %v", err)
	}
}
