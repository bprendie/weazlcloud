package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

var (
	ErrConflict         = errors.New("library path conflicts with an existing file or folder")
	ErrNotFound         = errors.New("library path does not exist")
	ErrDescendant       = errors.New("cannot move a folder into itself or a descendant")
	ErrUnknownReference = errors.New("catalog contains an unsupported storage reference")
	ErrRevisionOverflow = errors.New("catalog entry revision overflow")
	ErrRevisionMismatch = errors.New("catalog entry changed during storage commit")
)

const (
	ResticBackend = "restic"
	SharedBackend = "shared-object"
)

type Reference struct {
	Backend       string `json:"backend"`
	Version       uint16 `json:"version"`
	Snapshot      string `json:"snapshot"`
	Object        string `json:"object"`
	Operation     string `json:"operation,omitempty"`
	OwnerEntryID  string `json:"owner_entry_id,omitempty"`
	OwnerRevision uint64 `json:"owner_revision,omitempty"`
}

type File struct {
	PhotoPreviewUnsupported bool             `json:"photo_preview_unsupported,omitempty"`
	PhotoProcessingPending  bool             `json:"photo_processing_pending,omitempty"`
	PhotoPairAlbums         []string         `json:"photo_pair_albums,omitempty"`
	PhotoParentID           string           `json:"photo_parent_id,omitempty"`
	PhotoComponents         []PhotoComponent `json:"photo_components,omitempty"`
	DeviceID                string           `json:"device_id,omitempty"`
	DeviceAssetID           string           `json:"device_asset_id,omitempty"`
	SourceRevision          string           `json:"source_revision,omitempty"`
	EntryID                 string           `json:"entry_id,omitempty"`
	Revision                uint64           `json:"revision,omitempty"`
	Path                    string           `json:"path"`
	Folder                  bool             `json:"folder,omitempty"`
	Hidden                  bool             `json:"hidden,omitempty"`
	Size                    int64            `json:"size"`
	Mtime                   time.Time        `json:"mtime"`
	ImportedAt              time.Time        `json:"imported_at,omitempty"`
	CaptureTime             *time.Time       `json:"capture_time,omitempty"`
	CaptureOffsetMinutes    *int             `json:"capture_offset_minutes,omitempty"`
	CaptureSource           string           `json:"capture_source,omitempty"`
	CaptureUserCorrected    bool             `json:"capture_user_corrected,omitempty"`
	Width                   int              `json:"width,omitempty"`
	Height                  int              `json:"height,omitempty"`
	DurationMillis          int64            `json:"duration_millis,omitempty"`
	Orientation             int              `json:"orientation,omitempty"`
	PreferredPhoto          bool             `json:"preferred_photo,omitempty"`
	Camera                  string           `json:"camera,omitempty"`
	UserRotation            int              `json:"user_rotation,omitempty"`
	Favorite                bool             `json:"favorite,omitempty"`
	Archived                bool             `json:"archived,omitempty"`
	Caption                 string           `json:"caption,omitempty"`
	Hash                    string           `json:"hash"`
	Snap                    string           `json:"snap"`
	Object                  string           `json:"object,omitempty"`
	Reference               *Reference       `json:"reference,omitempty"`
	Present                 bool             `json:"present"`
	DeletedAt               *time.Time       `json:"deleted_at,omitempty"`
}

type tree struct {
	Schema      int                     `json:"schema,omitempty"`
	Collections collectionState         `json:"collections,omitempty"`
	Files       []File                  `json:"files"`
	Albums      []Album                 `json:"albums,omitempty"`
	Journal     Journal                 `json:"journal,omitempty"`
	Checkpoints map[string]SyncPosition `json:"device_checkpoints,omitempty"`
}

type Catalog struct {
	batchOnly        bool // private shadow transactions only
	collections      collectionState
	savedCollections collectionState
	mu               sync.Mutex
	path             string
	vault            *vault.Vault
	files            []File
	albums           []Album
	savedAlbums      []Album
	journal          Journal
	checkpoints      map[string]SyncPosition
	children         map[string][]File
	byPath           map[string]File
	summary          catalogSummary
	summaryReady     bool
	version          uint64
	diskInfo         os.FileInfo
}

type catalogSummary struct {
	logical, unique, trash int64
	trashCount             int
}

func New(path string, v *vault.Vault) *Catalog {
	return &Catalog{path: path, vault: v}
}

func (c *Catalog) Load() error {
	return c.load(true)
}

// LoadReadOnly upgrades legacy entries in memory without writing the catalog.
// Maintenance inventory uses it so a dry run never changes user data.
func (c *Catalog) LoadReadOnly() error { return c.load(false) }

func (c *Catalog) load(persistUpgrade bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files = nil
	c.collections, c.savedCollections = collectionState{}, collectionState{}
	c.albums = nil
	c.savedAlbums = nil
	c.journal = Journal{}
	c.checkpoints = nil
	c.children = nil
	c.byPath = nil
	c.summaryReady = false
	b, err := os.ReadFile(c.path)
	if os.IsNotExist(err) {
		c.files = nil
		c.diskInfo = nil
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := c.vault.Unwrap(b)
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	var t tree
	if err := json.Unmarshal(plain, &t); err != nil {
		return err
	}
	if t.Schema > 2 {
		return ErrCollectionSchema
	}
	c.collections = cloneCollectionState(t.Collections)
	c.savedCollections = cloneCollectionState(t.Collections)
	files, changed, err := upgradeFiles(t.Files)
	if err != nil {
		return err
	}
	c.albums = cloneAlbums(t.Albums)
	c.savedAlbums = cloneAlbums(t.Albums)
	c.journal = cloneJournal(t.Journal)
	c.checkpoints = t.Checkpoints
	if (changed || t.Schema < 2) && persistUpgrade {
		if t.Schema < 2 {
			if err := c.preserveMigrationRecovery(b); err != nil {
				return err
			}
		}
		if err := c.saveFilesLocked(files); err != nil {
			return err
		}
	}
	c.files = files
	c.children = indexChildren(files)
	c.byPath = indexPaths(files)
	c.diskInfo, _ = os.Stat(c.path)
	c.version++
	return nil
}

func (c *Catalog) List() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]File, 0, len(c.files))
	for _, f := range c.files {
		if f.Present {
			out = append(out, cloneFile(f))
		}
	}
	return out
}

func (c *Catalog) All() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]File, len(c.files))
	for i, f := range c.files {
		out[i] = cloneFile(f)
	}
	return out
}

func (c *Catalog) Trash() []File {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]File, 0)
	for _, f := range c.files {
		if !f.Present && f.DeletedAt != nil {
			out = append(out, cloneFile(f))
		}
	}
	return out
}

func (c *Catalog) Mkdir(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.files {
		if !f.Present {
			continue
		}
		if f.Path == path || (!f.Folder && strings.HasPrefix(path, f.Path+"/")) {
			return ErrConflict
		}
	}
	next := append([]File(nil), c.files...)
	f := File{Path: path, Folder: true, Mtime: time.Now().UTC(), Present: true}
	if err := assignIdentity(&f); err != nil {
		return err
	}
	next = append(next, f)
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}

func (c *Catalog) Rename(oldPath, newPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if oldPath == newPath {
		return nil
	}
	if strings.HasPrefix(newPath, oldPath+"/") {
		return ErrDescendant
	}
	found := false
	for _, f := range c.files {
		if f.Present && (f.Path == oldPath || strings.HasPrefix(f.Path, oldPath+"/")) {
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	for _, f := range c.files {
		if !f.Present || f.Path == oldPath || strings.HasPrefix(f.Path, oldPath+"/") {
			continue
		}
		if f.Path == newPath || strings.HasPrefix(f.Path, newPath+"/") || (!f.Folder && strings.HasPrefix(newPath, f.Path+"/")) {
			return ErrConflict
		}
	}
	next := append([]File(nil), c.files...)
	for i, f := range next {
		if !f.Present || (f.Path != oldPath && !strings.HasPrefix(f.Path, oldPath+"/")) {
			continue
		}
		if f.Revision == ^uint64(0) {
			return ErrRevisionOverflow
		}
		suffix := strings.TrimPrefix(f.Path, oldPath)
		next[i].Path = newPath + suffix
		next[i].Revision++
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}
