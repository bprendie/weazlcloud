package catalog

import (
	"errors"
	"sort"
	"strings"
)

var ErrCollectionSchema = errors.New("unsupported collection catalog schema")
var ErrSourceConflict = errors.New("source operation identity conflicts")

const CollectionPageMaximum = 200

type CollectionFolder struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Title    string `json:"title"`
	Position int    `json:"position"`
	Revision uint64 `json:"revision"`
}
type collectionState struct {
	Folders    []CollectionFolder       `json:"folders,omitempty"`
	Sources    map[string]SourceMapping `json:"sources,omitempty"`
	Operations map[string]SourceReceipt `json:"operations,omitempty"`
}

func cloneCollectionState(s collectionState) collectionState {
	s.Folders = append([]CollectionFolder(nil), s.Folders...)
	sources := make(map[string]SourceMapping, len(s.Sources))
	for k, v := range s.Sources {
		sources[k] = v
	}
	s.Sources = sources
	ops := make(map[string]SourceReceipt, len(s.Operations))
	for k, v := range s.Operations {
		ops[k] = v
	}
	s.Operations = ops
	return s
}
func (c *Catalog) validateCollectionParentLocked(parent, self string) error {
	seen := map[string]bool{self: true}
	for depth := 0; parent != ""; depth++ {
		if depth >= 63 || seen[parent] {
			return ErrDescendant
		}
		seen[parent] = true
		found := false
		for _, f := range c.collections.Folders {
			if f.ID == parent {
				parent = f.ParentID
				found = true
				break
			}
		}
		if !found {
			return ErrNotFound
		}
	}
	return nil
}
func (c *Catalog) mutateCollectionFolderLocked(f CollectionFolder) (CollectionFolder, error) {
	f.Title = strings.TrimSpace(f.Title)
	if f.Title == "" || len(f.Title) > 200 || f.Position < 0 || f.Position > 100000 {
		return CollectionFolder{}, ErrAlbumInvalid
	}
	if err := c.validateCollectionParentLocked(f.ParentID, f.ID); err != nil {
		return CollectionFolder{}, err
	}
	index := -1
	if f.ID == "" {
		id, err := newEntryID()
		if err != nil {
			return f, err
		}
		f.ID = "pc_" + id
		f.Revision = 1
	} else {
		for i, old := range c.collections.Folders {
			if old.ID == f.ID {
				if old.Revision != f.Revision {
					return f, ErrRevisionMismatch
				}
				if old.Revision == ^uint64(0) {
					return f, ErrRevisionOverflow
				}
				index = i
				f.Revision++
				break
			}
		}
		if index < 0 {
			return f, ErrNotFound
		}
	}
	// Reparenting a subtree must also respect the maximum descendant depth.
	old := c.collections
	c.collections = cloneCollectionState(old)
	if index < 0 {
		c.collections.Folders = append(c.collections.Folders, f)
	} else {
		c.collections.Folders[index] = f
	}
	for _, child := range c.collections.Folders {
		if err := c.validateCollectionParentLocked(child.ParentID, child.ID); err != nil {
			c.collections = old
			return f, err
		}
	}
	return f, nil
}
func (c *Catalog) SaveCollectionFolder(f CollectionFolder) (CollectionFolder, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.collections
	result, err := c.mutateCollectionFolderLocked(f)
	if err != nil {
		return result, err
	}
	if err = c.saveFilesLocked(c.files); err != nil {
		c.collections = old
	}
	return result, err
}
func (c *Catalog) DeleteCollectionFolder(id string, revision uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := -1
	for i, f := range c.collections.Folders {
		if f.ID == id {
			if f.Revision != revision {
				return ErrRevisionMismatch
			}
			index = i
		}
		if f.ParentID == id {
			return ErrConflict
		}
	}
	for _, a := range c.albums {
		if a.ParentID == id {
			return ErrConflict
		}
	}
	if index < 0 {
		return ErrNotFound
	}
	old := c.collections
	c.collections = cloneCollectionState(old)
	c.collections.Folders = append(c.collections.Folders[:index], c.collections.Folders[index+1:]...)
	if err := c.saveFilesLocked(c.files); err != nil {
		c.collections = old
		return err
	}
	return nil
}

type CollectionNode struct {
	ID     string            `json:"id"`
	Folder *CollectionFolder `json:"folder,omitempty"`
	Album  *Album            `json:"album,omitempty"`
}
type CollectionPage struct {
	Checkpoint string           `json:"checkpoint,omitempty"`
	Schema     int              `json:"schema"`
	Position   SyncPosition     `json:"position"`
	Nodes      []CollectionNode `json:"nodes"`
	Next       string           `json:"next,omitempty"`
	HasMore    bool             `json:"has_more"`
}

// Position must match exactly: a changed tree invalidates an in-flight snapshot.
func (c *Catalog) CollectionPage(after string, position *SyncPosition, limit int) (CollectionPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.journal.Epoch == "" {
		if err := c.saveFilesLocked(c.files); err != nil {
			return CollectionPage{}, err
		}
	}
	if position != nil && *position != c.journal.SyncPosition {
		return CollectionPage{}, ErrSyncExpired
	}
	nodes := []CollectionNode{}
	for _, f := range c.collections.Folders {
		f := f
		nodes = append(nodes, CollectionNode{ID: f.ID, Folder: &f})
	}
	for _, a := range c.albums {
		a := cloneAlbum(a)
		a.AssetIDs = nil
		a.CoverID = ""
		nodes = append(nodes, CollectionNode{ID: a.ID, Album: &a})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	start := sort.Search(len(nodes), func(i int) bool { return nodes[i].ID > after })
	end := min(start+max(1, min(limit, CollectionPageMaximum)), len(nodes))
	page := CollectionPage{Schema: 2, Position: c.journal.SyncPosition, Nodes: nodes[start:end], HasMore: end < len(nodes)}
	if page.HasMore {
		page.Next = nodes[end-1].ID
	}
	return page, nil
}
