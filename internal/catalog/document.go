package catalog

import (
	"encoding/json"
	"time"
)

// DecodeFilesJSON reads supported plaintext catalog wire shapes and rejects
// future schemas. This is a projection, not a round-trip writer: use Catalog
// mutations to retain collections, mappings, journals and checkpoints.
func DecodeFilesJSON(plain []byte) ([]File, error) {
	var document tree
	if err := json.Unmarshal(plain, &document); err != nil {
		return nil, err
	}
	files := make([]File, len(document.Files))
	for i, f := range document.Files {
		files[i] = cloneFile(f)
	}
	return files, nil
}

// SetTrashDate revises retention metadata for an existing tombstone without
// rewriting a files-only projection. No HTTP handler exposes this operation.
func (c *Catalog) SetTrashDate(id string, revision uint64, at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := append([]File(nil), c.files...)
	for i, f := range next {
		if f.EntryID != id {
			continue
		}
		if f.Present || f.DeletedAt == nil {
			return ErrConflict
		}
		if f.Revision != revision {
			return ErrRevisionMismatch
		}
		if f.Revision == ^uint64(0) {
			return ErrRevisionOverflow
		}
		f.DeletedAt = &at
		f.Revision++
		next[i] = f
		if err := c.saveFilesLocked(next); err != nil {
			return err
		}
		c.files = next
		return nil
	}
	return ErrNotFound
}
