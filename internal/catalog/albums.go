package catalog

import (
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

var (
	ErrAlbumNotFound = errors.New("photo album does not exist")
	ErrAlbumInvalid  = errors.New("photo album data is invalid")
)

type Album struct {
	ID          string   `json:"id"`
	Revision    uint64   `json:"revision"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	CoverID     string   `json:"cover_id,omitempty"`
	Position    int      `json:"position"`
	AssetIDs    []string `json:"asset_ids"`
}

func (c *Catalog) Albums() []Album {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := cloneAlbums(c.albums)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out
}

func (c *Catalog) SaveAlbum(album Album) (Album, error) {
	creating := album.ID == ""
	album.Title = strings.TrimSpace(album.Title)
	album.Description = strings.TrimSpace(album.Description)
	if album.Position < 0 || album.Position > 100000 || album.Title == "" || len(album.Title) > 200 || len(album.Description) > 4000 || len(album.AssetIDs) > 100_000 {
		return Album{}, ErrAlbumInvalid
	}
	if album.ID == "" {
		raw, err := cryptox.Random(16)
		if err != nil {
			return Album{}, err
		}
		album.ID = "pa_" + hex.EncodeToString(raw)
		album.Revision = 1
	}
	if len(album.ID) > 64 || !strings.HasPrefix(album.ID, "pa_") {
		return Album{}, ErrAlbumInvalid
	}
	seen := make(map[string]bool, len(album.AssetIDs))
	for _, id := range album.AssetIDs {
		if id == "" || len(id) > 128 || seen[id] {
			return Album{}, ErrAlbumInvalid
		}
		seen[id] = true
	}
	if album.AssetIDs != nil && album.CoverID != "" && !seen[album.CoverID] {
		return Album{}, ErrAlbumInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	validMembers := make(map[string]bool, len(c.files))
	for _, file := range c.files {
		if photoAlbumMember(file) {
			validMembers[file.EntryID] = true
		}
	}
	for _, id := range album.AssetIDs {
		if !validMembers[id] {
			return Album{}, ErrNotFound
		}
	}
	for i, old := range c.albums {
		if old.ID != album.ID {
			continue
		}
		if album.Revision != old.Revision {
			return Album{}, ErrRevisionMismatch
		}
		if old.Revision == ^uint64(0) {
			return Album{}, ErrRevisionOverflow
		}
		if album.AssetIDs == nil {
			album.AssetIDs = append([]string(nil), old.AssetIDs...)
		}
		if album.CoverID == "" {
			album.CoverID = old.CoverID
		}
		if album.CoverID != "" {
			valid := false
			for _, id := range album.AssetIDs {
				valid = valid || id == album.CoverID
			}
			if !valid {
				return Album{}, ErrAlbumInvalid
			}
		}
		album.Revision = old.Revision + 1
		if err := c.persistAlbumAtLocked(i, album); err != nil {
			return Album{}, err
		}
		return cloneAlbum(album), nil
	}
	if !creating {
		return Album{}, ErrAlbumNotFound
	}
	next := append(cloneAlbums(c.albums), cloneAlbum(album))
	previous := c.albums
	c.albums = next
	if err := c.saveFilesLocked(c.files); err != nil {
		c.albums = previous
		return Album{}, err
	}
	return cloneAlbum(album), nil
}

func (c *Catalog) DeleteAlbum(id string, revision uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, album := range c.albums {
		if album.ID != id {
			continue
		}
		if revision != 0 && revision != album.Revision {
			return ErrRevisionMismatch
		}
		previous := c.albums
		c.albums = append(cloneAlbums(c.albums[:i]), cloneAlbums(c.albums[i+1:])...)
		if err := c.saveFilesLocked(c.files); err != nil {
			c.albums = previous
			return err
		}
		return nil
	}
	return ErrAlbumNotFound
}

func (c *Catalog) ChangeAlbumMembers(id string, revision uint64, add, remove []string) (Album, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := -1
	for i := range c.albums {
		if c.albums[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return Album{}, ErrAlbumNotFound
	}
	old := c.albums[index]
	if old.Revision != revision {
		return Album{}, ErrRevisionMismatch
	}
	if len(add)+len(remove) > 100_000 {
		return Album{}, ErrAlbumInvalid
	}
	removed := make(map[string]bool, len(remove))
	for _, assetID := range remove {
		if assetID == "" || len(assetID) > 128 {
			return Album{}, ErrAlbumInvalid
		}
		removed[assetID] = true
	}
	members := make([]string, 0, len(old.AssetIDs)+len(add))
	seen := make(map[string]bool, len(old.AssetIDs)+len(add))
	for _, assetID := range old.AssetIDs {
		if !removed[assetID] {
			members = append(members, assetID)
			seen[assetID] = true
		}
	}
	validMembers := make(map[string]bool, len(c.files))
	for _, file := range c.files {
		if photoAlbumMember(file) {
			validMembers[file.EntryID] = true
		}
	}
	for _, assetID := range add {
		if assetID == "" || len(assetID) > 128 {
			return Album{}, ErrAlbumInvalid
		}
		if seen[assetID] {
			continue
		}
		if !validMembers[assetID] {
			return Album{}, ErrNotFound
		}
		members = append(members, assetID)
		seen[assetID] = true
	}
	if len(members) > 100_000 {
		return Album{}, ErrAlbumInvalid
	}
	if old.Revision == ^uint64(0) {
		return Album{}, ErrRevisionOverflow
	}
	updated := cloneAlbum(old)
	updated.Revision++
	updated.AssetIDs = members
	if removed[updated.CoverID] {
		updated.CoverID = ""
	}
	if updated.CoverID == "" && len(members) > 0 {
		updated.CoverID = members[0]
	}
	if err := c.persistAlbumAtLocked(index, updated); err != nil {
		return Album{}, err
	}
	return updated, nil
}

func (c *Catalog) persistAlbumAtLocked(index int, album Album) error {
	previous := c.albums
	c.albums = cloneAlbums(c.albums)
	c.albums[index] = cloneAlbum(album)
	if err := c.saveFilesLocked(c.files); err != nil {
		c.albums = previous
		return err
	}
	return nil
}

func cloneAlbums(albums []Album) []Album {
	out := make([]Album, len(albums))
	for i, album := range albums {
		out[i] = cloneAlbum(album)
	}
	return out
}

func cloneAlbum(album Album) Album {
	album.AssetIDs = append([]string(nil), album.AssetIDs...)
	return album
}
