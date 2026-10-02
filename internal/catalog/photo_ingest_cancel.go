package catalog

import "strings"

// DiscardPhotoPending forgets only verified private component rows. Published
// originals, including moved or trashed originals, are never eligible.
func (c *Catalog) DiscardPhotoPending(parts []PhotoIngestFile) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := make(map[string]PhotoIngestFile, len(parts))
	for _, p := range parts {
		if !strings.HasPrefix(p.From, ".weazl-mobile-pending/") {
			return ErrConflict
		}
		want[p.From] = p
	}
	next := make([]File, 0, len(c.files))
	for _, f := range c.files {
		p, ok := want[f.Path]
		if !ok {
			next = append(next, f)
			continue
		}
		if f.Folder || f.DeviceID != "" || f.PhotoParentID != "" || len(f.PhotoComponents) != 0 || f.Size != p.Size || f.Hash != p.Hash {
			return ErrConflict
		}
	}
	if len(next) == len(c.files) {
		return nil
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}
