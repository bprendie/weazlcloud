package catalog

import (
	"errors"
)

var ErrInvalidHiddenEntry = errors.New("only folders may be hidden")

// SetFolderHidden changes the encrypted catalog visibility flag by stable
// folder identity. It leaves folder identity and storage references untouched.
func (c *Catalog) SetFolderHidden(entryID string, revision uint64, hidden bool) (File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := append([]File(nil), c.files...)
	for i := range next {
		f := &next[i]
		if f.EntryID != entryID || f.Revision != revision || !f.Present || !f.Folder {
			continue
		}
		if f.Hidden == hidden {
			return cloneFile(*f), nil
		}
		if f.Revision == ^uint64(0) {
			return File{}, ErrRevisionOverflow
		}
		f.Hidden = hidden
		f.Revision++
		if err := c.saveFilesLocked(next); err != nil {
			return File{}, err
		}
		c.files = next
		return cloneFile(*f), nil
	}
	return File{}, ErrRevisionMismatch
}

// SetPhotoFlags updates a revision-bound selection in one encrypted catalog save.
// Nil flags retain their current value. Storage references and identities stay intact.
func (c *Catalog) SetPhotoFlags(files []File, hidden, archived *bool) ([]File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	expected := make(map[string]uint64, len(files))
	for _, f := range files {
		expected[f.EntryID] = f.Revision
	}
	next := append([]File(nil), c.files...)
	changed := []File{}
	for i := range next {
		f := &next[i]
		revision, ok := expected[f.EntryID]
		if !ok {
			continue
		}
		if !f.Present || f.Folder || f.Revision != revision {
			return nil, ErrRevisionMismatch
		}
		delete(expected, f.EntryID)
		if (hidden == nil || f.Hidden == *hidden) && (archived == nil || f.Archived == *archived) {
			continue
		}
		if f.Revision == ^uint64(0) {
			return nil, ErrRevisionOverflow
		}
		if hidden != nil {
			f.Hidden = *hidden
		}
		if archived != nil {
			f.Archived = *archived
		}
		f.Revision++
		changed = append(changed, cloneFile(*f))
	}
	if len(expected) != 0 {
		return nil, ErrRevisionMismatch
	}
	if len(changed) != 0 {
		if err := c.saveFilesLocked(next); err != nil {
			return nil, err
		}
		c.files = next
	}
	return changed, nil
}
