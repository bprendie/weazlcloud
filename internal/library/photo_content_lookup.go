package library

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/vault"
)

const PhotoContentLookupLimit = 200
const PhotoContentMatchLimit = 20

var ErrPhotoContentLookup = errors.New("supply 1-200 SHA-256 and positive-size photo checks")

type PhotoContentKey struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type PhotoContentMatch struct {
	AssetID          string `json:"asset_id"`
	Revision         uint64 `json:"revision"`
	ComponentID      string `json:"component_id"`
	ComponentAssetID string `json:"component_asset_id"`
	Hidden           bool   `json:"hidden"`
	Archived         bool   `json:"archived"`
}
type PhotoContentResult struct {
	PhotoContentKey
	Exists         bool                `json:"exists"`
	Matches        []PhotoContentMatch `json:"matches"`
	MatchCount     int                 `json:"match_count"`
	HasMoreMatches bool                `json:"has_more_matches"`
}
type PhotoContentPage struct {
	Results    []PhotoContentResult `json:"results"`
	Generation uint64               `json:"generation"`
}

func normalizePhotoContentKey(key PhotoContentKey) (PhotoContentKey, bool) {
	if len(key.SHA256) != 64 || key.Size <= 0 {
		return key, false
	}
	if _, err := hex.DecodeString(key.SHA256); err != nil {
		return key, false
	}
	key.SHA256 = strings.ToLower(key.SHA256)
	return key, true
}

// Maintained with the owner photo index; stores identities, not original bytes.
func (l *Library) indexPhotoContentLocked(f catalog.File, remove bool) {
	key, valid := normalizePhotoContentKey(PhotoContentKey{f.Hash, f.Size})
	if !valid || f.Folder || !f.Present || !(photoMedia(f.Path) || f.PhotoPreviewUnsupported || f.PhotoParentID != "") {
		return
	}
	ids := l.photoContentIndex[key]
	for i, id := range ids {
		if id != f.EntryID {
			continue
		}
		if !remove {
			return
		}
		ids[i] = ids[len(ids)-1]
		ids[len(ids)-1] = ""
		ids = ids[:len(ids)-1]
		if len(ids) == 0 {
			delete(l.photoContentIndex, key)
		} else {
			l.photoContentIndex[key] = ids
		}
		return
	}
	if remove {
		return
	}
	if l.photoContentIndex == nil {
		l.photoContentIndex = make(map[PhotoContentKey][]string)
	}
	l.photoContentIndex[key] = append(ids, f.EntryID)
}

// PhotoContentLookup is an owner-scoped, read-only catalog observation. It neither
// reserves a match nor creates a backup receipt or source/album association.
func (l *Library) PhotoContentLookup(ctx context.Context, keys []PhotoContentKey, includeHidden bool) (PhotoContentPage, error) {
	if len(keys) < 1 || len(keys) > PhotoContentLookupLimit {
		return PhotoContentPage{}, ErrPhotoContentLookup
	}
	normalized := make([]PhotoContentKey, len(keys))
	for i, key := range keys {
		var ok bool
		normalized[i], ok = normalizePhotoContentKey(key)
		if !ok {
			return PhotoContentPage{}, ErrPhotoContentLookup
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.vault.Unlocked() {
		return PhotoContentPage{}, vault.ErrLocked
	}
	if err := ctx.Err(); err != nil {
		return PhotoContentPage{}, err
	}
	if err := l.ensurePhotoIndexLocked(ctx); err != nil {
		return PhotoContentPage{}, err
	}
	l.photoMu.Lock()
	defer l.photoMu.Unlock()
	page := PhotoContentPage{Results: make([]PhotoContentResult, 0, len(keys)), Generation: l.photoEpoch}
	cached := make(map[PhotoContentKey]PhotoContentResult)
	for _, key := range normalized {
		if err := ctx.Err(); err != nil {
			return PhotoContentPage{}, err
		}
		result, ok := cached[key]
		if !ok {
			result = PhotoContentResult{PhotoContentKey: key, Matches: []PhotoContentMatch{}}
			for _, id := range l.photoContentIndex[key] {
				if err := ctx.Err(); err != nil {
					return PhotoContentPage{}, err
				}
				f, ok := l.photoByID[id]
				if !ok {
					continue
				}
				parent, component := f, "original"
				if f.PhotoParentID != "" {
					parent, ok = l.photoByID[f.PhotoParentID]
					if !ok {
						continue
					}
					component = ""
					for _, part := range parent.PhotoComponents {
						if part.AssetID == f.EntryID {
							component = part.ID
							break
						}
					}
					if component == "" {
						continue
					}
				}
				hidden := l.photoPathHiddenLocked(parent.Path) || l.photoPathHiddenLocked(f.Path)
				if hidden && !includeHidden {
					continue
				}
				result.MatchCount++
				result.Matches = append(result.Matches, PhotoContentMatch{parent.EntryID, parent.Revision, component, f.EntryID, hidden, parent.Archived})
				sort.Slice(result.Matches, func(i, j int) bool {
					a, b := result.Matches[i], result.Matches[j]
					if a.AssetID != b.AssetID {
						return a.AssetID < b.AssetID
					}
					return a.ComponentAssetID < b.ComponentAssetID
				})
				if len(result.Matches) > PhotoContentMatchLimit {
					result.Matches = result.Matches[:PhotoContentMatchLimit]
				}
			}
			result.Exists = result.MatchCount > 0
			result.HasMoreMatches = result.MatchCount > len(result.Matches)
			cached[key] = result
		}
		page.Results = append(page.Results, result)
	}
	return page, nil
}
