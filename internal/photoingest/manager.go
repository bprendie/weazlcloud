package photoingest

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/upload"
	"github.com/bprendie/weazlcloud/internal/users"
)

type Receipt struct {
	Version         int                      `json:"version"`
	FinalizeCapture *catalog.CaptureMetadata `json:"finalize_capture,omitempty"`
	ID              string                   `json:"id"`
	Spec            Spec                     `json:"spec"`
	Uploads         []string                 `json:"uploads"`
	AssetID         string                   `json:"asset_id,omitempty"`
	Revision        uint64                   `json:"revision,omitempty"`
	Path            string                   `json:"path,omitempty"`
	Status          string                   `json:"status"`
	CreatedAt       time.Time                `json:"created_at"`
}

type ComponentView struct {
	ID         string `json:"id"`
	Offset     int64  `json:"offset"`
	Size       int64  `json:"size"`
	Status     string `json:"status"`
	ChunkLimit int64  `json:"chunk_limit"`
}

type View struct {
	ProcessingState string             `json:"processing_state,omitempty"`
	Upload          upload.SessionView `json:"upload"`
	Component       ComponentView      `json:"component"`
	Components      []ComponentView    `json:"components"`
	AssetID         string             `json:"asset_id,omitempty"`
	DeviceAssetID   string             `json:"device_asset_id"`
	SourceRevision  string             `json:"source_revision"`
	Path            string             `json:"path,omitempty"`
	Revision        uint64             `json:"revision,omitempty"`
	Status          string             `json:"status"`
}

type gate struct {
	sync.Mutex
	refs int
}
type Manager struct {
	uploads *upload.Manager
	mu      sync.Mutex
	gates   map[string]*gate
}

func New(uploads *upload.Manager) *Manager {
	return &Manager{uploads: uploads, gates: make(map[string]*gate)}
}
func (m *Manager) lock(owner, id string) func() {
	key := owner + ":" + id
	m.mu.Lock()
	g := m.gates[key]
	if g == nil {
		g = &gate{}
		m.gates[key] = g
	}
	g.refs++
	m.mu.Unlock()
	g.Lock()
	return func() {
		g.Unlock()
		m.mu.Lock()
		g.refs--
		if g.refs == 0 {
			delete(m.gates, key)
		}
		m.mu.Unlock()
	}
}

func receiptPath(res *filesvc.Resource, id string) string {
	return filepath.Join(res.Lib.PhotoIngestDir(), id+".enc")
}
func validID(id string) bool { b, err := hex.DecodeString(id); return err == nil && len(b) == 16 }
func load(res *filesvc.Resource, id string) (Receipt, error) {
	if !validID(id) {
		return Receipt{}, upload.ErrNotFound
	}
	f, err := os.Open(receiptPath(res, id))
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, upload.ErrNotFound
	}
	if err != nil {
		return Receipt{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 128<<10))
	if err != nil || len(raw) >= 128<<10 {
		return Receipt{}, upload.ErrCorrupt
	}
	plain, err := res.Vault.Unwrap(raw)
	if err != nil {
		return Receipt{}, err
	}
	defer clear(plain)
	var receipt Receipt
	if json.Unmarshal(plain, &receipt) != nil || receipt.ID != id || receipt.Version != 1 || len(receipt.Uploads) != len(receipt.Spec.Components) || len(receipt.Uploads) < 1 || len(receipt.Uploads) > 2 {
		return Receipt{}, upload.ErrCorrupt
	}
	return receipt, nil
}
func save(res *filesvc.Resource, receipt Receipt) error {
	plain, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	defer clear(plain)
	raw, err := res.Vault.Wrap(plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(res.Lib.PhotoIngestDir(), 0700); err != nil {
		return err
	}
	return cryptox.AtomicWrite(receiptPath(res, receipt.ID), raw, 0600)
}

func (m *Manager) Create(ctx context.Context, res *filesvc.Resource, user users.User, spec Spec) (View, error) {
	if err := spec.Normalize(); err != nil {
		return View{}, err
	}
	if _, err := res.Lib.ResolvePhotoUploadRoot(ctx, spec.RootID); err != nil {
		return View{}, err
	}
	if err := res.Lib.ValidatePhotoUploadAlbums(ctx, spec.AlbumIDs); err != nil {
		return View{}, err
	}
	identity, _ := json.Marshal([]string{spec.DeviceID, spec.DeviceAssetID, spec.SourceRevision})
	hash, err := res.Vault.Fingerprint("photo-ingest", identity)
	if err != nil {
		return View{}, err
	}
	id := hex.EncodeToString(hash[:16])
	clear(hash)
	release := m.lock(user.ID, id)
	defer release()
	receipt, err := load(res, id)
	if err == nil {
		if !reflect.DeepEqual(spec, receipt.Spec) {
			return View{}, upload.ErrIdempotencyConflict
		}
	} else if errors.Is(err, upload.ErrNotFound) {
		receipt = Receipt{Version: 1, ID: id, Spec: spec, Uploads: make([]string, len(spec.Components)), Status: "uploading", CreatedAt: time.Now().UTC()}
		if err := save(res, receipt); err != nil {
			return View{}, err
		}
	} else {
		return View{}, err
	}
	if receipt.Status == "cancelled" {
		return View{}, upload.ErrNotFound
	}
	if receipt.Status == "stored" {
		return m.view(res, user, receipt)
	}
	for i, part := range spec.Components {
		if receipt.Uploads[i] != "" {
			continue
		}
		target := pendingPath(receipt, part)
		view, err := m.uploads.CreateIdempotent(user, target, part.Size, part.SHA256, "photo-ingest:"+id+":"+part.ID)
		if err != nil {
			return View{}, err
		}
		receipt.Uploads[i] = view.ID
		if err := save(res, receipt); err != nil {
			return View{}, err
		}
	}
	return m.view(res, user, receipt)
}

func pendingPath(receipt Receipt, part Component) string {
	return ".weazl-mobile-pending/" + receipt.ID + "/" + part.Filename
}

func (m *Manager) view(res *filesvc.Resource, user users.User, receipt Receipt) (View, error) {
	view := View{AssetID: receipt.AssetID, DeviceAssetID: receipt.Spec.DeviceAssetID, SourceRevision: receipt.Spec.SourceRevision, Path: receipt.Path, Revision: receipt.Revision, Status: receipt.Status,
		Upload: upload.SessionView{ID: receipt.ID, Path: receipt.Path, Status: receipt.Status, CreatedAt: receipt.CreatedAt}}
	for i, part := range receipt.Spec.Components {
		component := ComponentView{ID: part.ID, Size: part.Size, ChunkLimit: upload.MaxChunkBytes, Status: "uploading"}
		if receipt.Status == "stored" {
			component.Offset, component.Status = part.Size, "complete"
		} else if receipt.Uploads[i] != "" {
			uploadView, err := m.uploads.Status(user, receipt.Uploads[i])
			if err != nil {
				return View{}, err
			}
			component.Offset, component.Status = uploadView.Offset, uploadView.Status
		}
		view.Components = append(view.Components, component)
		view.Upload.Offset += component.Offset
		view.Upload.Size += component.Size
	}
	if receipt.Status == "stored" {
		view.ProcessingState, _ = res.Lib.PhotoProcessingState(context.Background(), receipt.AssetID)
	}
	view.Component = view.Components[0]
	if view.Upload.Offset == view.Upload.Size && receipt.Status == "uploading" {
		view.Upload.Status = "ready"
	}
	return view, nil
}
