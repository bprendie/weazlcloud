package catalog

import (
	"fmt"
	"path"
	"strings"
)

// Relocate atomically changes display paths while preserving storage refs.
// Empty destinations are allowed only for wrapper folders. Callers must hold
// exclusive maintenance access and back up the encrypted catalog beforehand.
func (c *Catalog) Relocate(mapping map[string]string, apply bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := make([]File, 0, len(c.files))
	seen := make(map[string]File)
	for _, f := range c.files {
		target, changed := mapping[f.Path]
		if !changed {
			target = f.Path
		}
		if target == "" {
			if !f.Folder {
				return fmt.Errorf("cannot remove file %q", f.Path)
			}
			continue
		}
		if path.Clean(target) != target || path.IsAbs(target) || target == "." || target == ".." || strings.HasPrefix(target, "../") || strings.ContainsAny(target, "\\\x00") {
			return fmt.Errorf("invalid destination %q", target)
		}
		if f.Present {
			if _, exists := seen[target]; exists {
				return fmt.Errorf("%w: %s", ErrConflict, target)
			}
			seen[target] = f
		}
		if f.Path != target {
			if f.Revision == ^uint64(0) {
				return ErrRevisionOverflow
			}
			f.Path = target
			f.Revision++
		}
		next = append(next, f)
	}
	for name := range seen {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			f, ok := seen[parent]
			if !ok || !f.Folder {
				return fmt.Errorf("missing or nonfolder parent %q", parent)
			}
		}
	}
	if !apply {
		return nil
	}
	if err := c.saveFilesLocked(next); err != nil {
		return err
	}
	c.files = next
	return nil
}
