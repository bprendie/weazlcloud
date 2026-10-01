package photos

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

type SourceRoot struct {
	ID       string
	EntryID  string
	Path     string
	Included bool
}

type Component struct {
	ID       string
	EntryID  string
	Revision uint64
	Path     string
	Kind     string
	Hash     string
}

type Album struct {
	ID          string
	OwnerID     string
	Title       string
	Description string
	CoverID     string
	Position    int
	AssetIDs    []string
	Source      string
}

type Asset struct {
	ID               string
	OwnerID          string
	EntryID          string
	Revision         uint64
	Path             string
	Hash             string
	DuplicateGroupID string
	MediaType        string
	ImportedAt       time.Time
	ModifiedAt       time.Time
	Capture          *Capture
	Components       []Component
	SourceRootID     string
	AlbumIDs         []string
	Width            int
	Height           int
	DurationMillis   int64
	Orientation      int
	UserRotation     int
	PreferredPhoto   bool
	Favorite         bool
	Archived         bool
	Trashed          bool
	Caption          string
	Camera           string
	DeviceID         string
	DeviceAssetID    string
	SourceRevision   string
	DerivativeStates map[string]string
}

type Projection struct {
	OwnerID string
	Roots   []SourceRoot
	Assets  []Asset
	Albums  []Album
}

func StableAssetID(entryID string) string {
	if entryID != "" {
		return "asset:" + entryID
	}
	return "asset:" + stableDigest("legacy-entry")
}

func StableAlbumID(folderEntryID, albumPath string) string {
	if folderEntryID != "" {
		return "album:" + folderEntryID
	}
	return "album:" + stableDigest("path:"+albumPath)
}

func StableSourceRootID(entryID, rootPath string) string {
	if entryID != "" {
		return "root:" + entryID
	}
	if strings.TrimSpace(rootPath) == "Photos/" {
		return "root:photos"
	}
	return "root:" + stableDigest("path:"+rootPath)
}

func BuildProjection(ownerID string, files []catalog.File, roots []SourceRoot) Projection {
	if len(roots) == 0 {
		roots = []SourceRoot{{ID: "root:photos", Path: "Photos/", Included: true}}
	}
	roots = normalizeRoots(roots)
	projection := Projection{OwnerID: ownerID, Roots: append([]SourceRoot(nil), roots...)}
	folderIDs := make(map[string]string)
	byID := make(map[string]catalog.File, len(files))
	for _, file := range files {
		byID[file.EntryID] = file
		if file.Folder {
			folderIDs[file.Path] = file.EntryID
		}
	}
	for _, file := range files {
		if file.Folder || file.PhotoParentID != "" || !isPhotoPath(file.Path) {
			continue
		}
		rootID := ""
		for _, root := range roots {
			if root.Included && (file.Path == root.Path || strings.HasPrefix(file.Path, root.Path)) {
				rootID = root.ID
				break
			}
		}
		if rootID == "" {
			continue
		}
		asset := Asset{
			ID: StableAssetID(file.EntryID), OwnerID: ownerID, EntryID: file.EntryID,
			Revision: file.Revision, Path: file.Path, Hash: file.Hash,
			DuplicateGroupID: duplicateGroupID(file.Hash), MediaType: mediaTypeForPath(file.Path),
			ImportedAt: file.ImportedAt, ModifiedAt: file.Mtime,
			SourceRootID: rootID, Width: file.Width, Height: file.Height,
			DurationMillis: file.DurationMillis, Orientation: file.Orientation, UserRotation: file.UserRotation, PreferredPhoto: file.PreferredPhoto,
			Favorite: file.Favorite, Archived: file.Archived, Trashed: !file.Present,
			Caption: file.Caption, Camera: file.Camera, DeviceID: file.DeviceID, DeviceAssetID: file.DeviceAssetID, SourceRevision: file.SourceRevision, DerivativeStates: make(map[string]string),
		}
		if captureTime, known := CanonicalCaptureTime(captureFromFile(file)); known {
			capture := Capture{Time: captureTime, OffsetMinutes: file.CaptureOffsetMinutes, Source: file.CaptureSource, UserCorrected: file.CaptureUserCorrected}
			asset.Capture = &capture
		}
		asset.Components = projectionComponents(file, byID)
		projection.Assets = append(projection.Assets, asset)
	}
	albumByPath := make(map[string]*Album)
	assetIndex := make(map[string]int, len(projection.Assets))
	for i := range projection.Assets {
		assetIndex[projection.Assets[i].ID] = i
	}
	for _, asset := range projection.Assets {
		rootPath := "Photos/"
		for _, root := range projection.Roots {
			if root.ID == asset.SourceRootID {
				rootPath = root.Path
				break
			}
		}
		rel := strings.TrimPrefix(asset.Path, rootPath)
		folder := strings.SplitN(rel, "/", 2)[0]
		if folder == "" {
			continue
		}
		albumPath := rootPath + folder
		album := albumByPath[albumPath]
		if album == nil {
			album = &Album{ID: StableAlbumID(folderIDs[albumPath], albumPath), OwnerID: ownerID, Title: folder, Source: "takeout-folder"}
			albumByPath[albumPath] = album
			projection.Albums = append(projection.Albums, *album)
		}
		album.AssetIDs = append(album.AssetIDs, asset.ID)
		projection.Assets[assetIndex[asset.ID]].AlbumIDs = append(projection.Assets[assetIndex[asset.ID]].AlbumIDs, album.ID)
		for i := range projection.Albums {
			if projection.Albums[i].ID == album.ID {
				projection.Albums[i].AssetIDs = append(projection.Albums[i].AssetIDs[:0], album.AssetIDs...)
				break
			}
		}
	}
	sort.Slice(projection.Assets, func(i, j int) bool { return projection.Assets[i].ID < projection.Assets[j].ID })
	sort.Slice(projection.Albums, func(i, j int) bool { return projection.Albums[i].ID < projection.Albums[j].ID })
	return projection
}

func captureFromFile(file catalog.File) *Capture {
	if file.CaptureTime == nil {
		return nil
	}
	return &Capture{Time: *file.CaptureTime}
}

func normalizeRoots(roots []SourceRoot) []SourceRoot {
	out := make([]SourceRoot, len(roots))
	for i, root := range roots {
		root.Path = strings.Trim(strings.TrimSpace(root.Path), "/") + "/"
		if root.Path == "/" {
			root.Path = ""
		}
		if root.ID == "" {
			root.ID = StableSourceRootID(root.EntryID, root.Path)
		}
		out[i] = root
	}
	return out
}

func isPhotoPath(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".heif", ".avif", ".tif", ".tiff", ".mp4", ".mov", ".m4v", ".webm", ".mkv":
		return true
	default:
		return false
	}
}

func mediaTypeForPath(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".heic", ".heif":
		return "image/heic"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}

func duplicateGroupID(hash string) string {
	if hash == "" {
		return ""
	}
	return "duplicate:" + hash
}

func stableDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
