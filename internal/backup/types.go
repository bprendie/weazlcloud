// Package backup coordinates one-way owner/device source imports. All private
// state is owner-vault encrypted; Manager retains no decrypted state.
package backup

import (
	"errors"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/upload"
)

var (
	ErrUnsupportedVersion  = errors.New("unsupported backup record version")
	ErrInvalid             = errors.New("invalid backup specification")
	ErrNotFound            = errors.New("backup resource not found")
	ErrIdempotencyConflict = errors.New("backup revision identity reused with different specification")
	ErrPaused              = errors.New("backup source paused or detached")
	ErrStale               = catalog.ErrRevisionMismatch
	ErrChecksum            = library.ErrBackupChecksum
)

type Spec struct {
	DeviceID         string    `json:"device_id"`
	SourceID         string    `json:"source_id"`
	ItemID           string    `json:"source_item_id"`
	SourceRevision   string    `json:"source_revision"`
	RelativePath     string    `json:"relative_path"`
	Filename         string    `json:"filename,omitempty"`
	Kind             string    `json:"kind"` // file or folder
	Size             int64     `json:"size"`
	SHA256           string    `json:"sha256"`
	Mtime            time.Time `json:"mtime"`
	ExpectedEntryID  string    `json:"expected_entry_id,omitempty"`
	ExpectedRevision uint64    `json:"expected_revision,omitempty"`
	Transport        string    `json:"transport,omitempty"`
}

type Source struct {
	DeviceID      string                  `json:"device_id"`
	ID            string                  `json:"source_id"`
	DestinationID string                  `json:"destination_id"`
	Name          string                  `json:"name"`
	Status        string                  `json:"status"` // active, paused, detached
	Revision      uint64                  `json:"revision"`
	Destination   catalog.File            `json:"-"`
	Items         map[string]catalog.File `json:"-"`
}

// sourceRecord keeps private mappings in the encrypted source file.
type sourceRecord struct {
	Version     int                     `json:"version"`
	Type        string                  `json:"type"`
	Source      Source                  `json:"source"`
	Destination catalog.File            `json:"destination"`
	Items       map[string]catalog.File `json:"items"`
	Pending     string                  `json:"pending,omitempty"`
}

type View struct {
	ID        string        `json:"id"`
	ReceiptID string        `json:"receipt_id"`
	Status    string        `json:"status"`
	Spec      Spec          `json:"spec"`
	UploadID  string        `json:"upload_id,omitempty"`
	File      *catalog.File `json:"file,omitempty"`
}

type operation struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	View
	Plan      catalog.BackupMutation `json:"plan"`
	SourceKey string                 `json:"source_key"`
	PrimaryID string                 `json:"primary_id"`
	StorageID string                 `json:"storage_id,omitempty"`
}

type Manager struct {
	uploads     *upload.Manager
	commitGuard func(func() error) error
}

func New(uploads *upload.Manager) *Manager { return &Manager{uploads: uploads} }

// SetCommitGuard installs the final device/account validity barrier. The guard
// executes only the catalog CAS and must not call back into the coordinator.
// Install before concurrent use. Acquire the users grant inside this callback,
// rather than around the whole call, to preserve library/users lock order.
func (m *Manager) SetCommitGuard(guard func(func() error) error) { m.commitGuard = guard }
