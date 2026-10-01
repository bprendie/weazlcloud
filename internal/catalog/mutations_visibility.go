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
