package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/users"
)

const trashLifetime = 30 * 24 * time.Hour

type Report struct {
	Action               string                       `json:"action"`
	Users                int                          `json:"users"`
	DisabledUsers        int                          `json:"disabled_users"`
	BlockedUsers         int                          `json:"blocked_users"`
	LiveFiles            int                          `json:"live_files"`
	TrashFiles           int                          `json:"trash_files"`
	ExpiredTrashFiles    int                          `json:"expired_trash_files"`
	Folders              int                          `json:"folders"`
	SharedFiles          int                          `json:"shared_files"`
	EligibleFiles        int                          `json:"eligible_files"`
	RemainingFiles       int                          `json:"remaining_files"`
	MigratedFiles        int                          `json:"migrated_files"`
	CompletedFiles       int                          `json:"completed_files"`
	FailedFiles          int                          `json:"failed_files"`
	CompletedBytes       int64                        `json:"completed_bytes"`
	BlockedFiles         int                          `json:"blocked_files"`
	StagedRecords        int                          `json:"staged_records"`
	PendingUploads       int                          `json:"pending_uploads"`
	LegacySnapshots      int                          `json:"legacy_snapshots"`
	UnassignedLegacy     bool                         `json:"unassigned_legacy_catalog"`
	LogicalBytes         int64                        `json:"logical_bytes"`
	SourceAllocatedBytes int64                        `json:"source_allocated_bytes"`
	WorstCaseDestination int64                        `json:"worst_case_destination_bytes"`
	PeakFileReservation  int64                        `json:"peak_file_reservation_bytes"`
	Paused               bool                         `json:"paused"`
	Journal              []sharedstore.MigrationTally `json:"journal,omitempty"`
}

func Run(ctx context.Context, dataDir, action string, out io.Writer) error {
	if action == "pause" {
		return os.WriteFile(filepath.Join(dataDir, "migration.pause"), []byte("paused\n"), 0o600)
	}
	if action == "retire" {
		return errors.New("legacy retirement is disabled until D6 rollback and recovery gates pass")
	}
	if !validAction(action) {
		return errors.New("migration action must be dry-run, status, start, pause, resume, verify, or retire")
	}
	if action == "start" || action == "resume" || action == "verify" || action == "dry-run" {
		if err := requireOffline("127.0.0.1:7272", "127.0.0.1:7273", "127.0.0.1:7274"); err != nil {
			return err
		}
	}
	if (action == "start" || action == "resume") && exists(filepath.Join(dataDir, "catalog.enc")) {
		report := Report{Action: action, UnassignedLegacy: true}
		return writeReport(out, report, errors.New("legacy root catalog must be assigned to an account before migration"))
	}
	if action == "start" || action == "resume" || action == "verify" {
		lock, err := acquireLock(dataDir)
		if err != nil {
			return err
		}
		defer lock()
	}
	if action == "resume" {
		_ = os.Remove(filepath.Join(dataDir, "migration.pause"))
	}
	newUsers := users.NewReadOnly
	if action == "start" || action == "resume" {
		newUsers = users.New
	}
	storeUsers, err := newUsers(filepath.Join(dataDir, "users.json"), filepath.Join(dataDir, "users"))
	if err != nil {
		return err
	}
	if action == "start" && exists(filepath.Join(dataDir, "migration.pause")) {
		report, reportErr := inventoryAll(ctx, dataDir, storeUsers, "start")
		return writeReport(out, report, reportErr)
	}
	if action == "start" || action == "resume" || action == "verify" {
		return runSharedAction(ctx, dataDir, action, storeUsers, out)
	}
	report, err := inventoryAll(ctx, dataDir, storeUsers, action)
	if action == "status" {
		report.Journal, _ = readJournal(dataDir)
		summarizeJournal(&report)
	}
	return writeReport(out, report, err)
}

func runSharedAction(ctx context.Context, dataDir, action string, storeUsers *users.Store, out io.Writer) error {
	shared, err := sharedstore.Open(dataDir, sharedstore.Options{})
	if err != nil {
		return err
	}
	defer shared.Close()
	var report Report
	if action == "verify" {
		report, err = verifyAll(ctx, storeUsers, shared)
	} else {
		report, err = migrateAll(ctx, dataDir, storeUsers, shared)
	}
	report.Journal, _ = shared.MigrationStatus(ctx)
	summarizeJournal(&report)
	return writeReport(out, report, err)
}

func summarizeJournal(report *Report) {
	for _, item := range report.Journal {
		switch item.State {
		case "complete":
			report.CompletedFiles += int(item.Items)
			report.CompletedBytes += item.Bytes
		case "blocked":
			report.FailedFiles += int(item.Items)
		}
	}
}

func validAction(action string) bool {
	switch action {
	case "dry-run", "status", "start", "resume", "verify":
		return true
	default:
		return false
	}
}

func readJournal(dataDir string) ([]sharedstore.MigrationTally, error) {
	path := filepath.Join(dataDir, "shared-index", "index.db")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String() + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT state,error_category,count(*),coalesce(sum(copied_bytes),0) FROM migration_items GROUP BY state,error_category ORDER BY state,error_category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sharedstore.MigrationTally
	for rows.Next() {
		var row sharedstore.MigrationTally
		if err = rows.Scan(&row.State, &row.ErrorCategory, &row.Items, &row.Bytes); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func requireOffline(addresses ...string) error {
	for _, address := range addresses {
		conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return errors.New("migration requires the Desk, grab, and WebDAV services to be stopped")
		}
	}
	return nil
}

func acquireLock(dataDir string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(dataDir, "migration.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("another migration operation is already running")
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func errorCategory(err error) string {
	switch {
	case errors.Is(err, quota.ErrExceeded):
		return "workspace"
	case errors.Is(err, catalog.ErrRevisionMismatch):
		return "catalog-changed"
	case strings.Contains(strings.ToLower(err.Error()), "metadata"):
		return "source-metadata"
	default:
		return "copy-or-verify"
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func writeReport(out io.Writer, report Report, err error) error {
	if encodeErr := json.NewEncoder(out).Encode(report); encodeErr != nil {
		return encodeErr
	}
	return err
}
