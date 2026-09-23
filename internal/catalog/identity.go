package catalog

import (
	"encoding/hex"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func newEntryID() (string, error) {
	id, err := cryptox.Random(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(id), nil
}

// NextIdentity reserves the immutable entry tuple a backend write must bind to.
// The caller serializes the later Put with the library mutation lock.
func (c *Catalog) NextIdentity(path string) (string, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.files {
		if f.Path == path && f.Present {
			if f.Revision == ^uint64(0) {
				return "", 0, ErrRevisionOverflow
			}
			return f.EntryID, f.Revision + 1, nil
		}
	}
	id, err := newEntryID()
	if err != nil {
		return "", 0, err
	}
	return id, 1, nil
}

func upgradeFiles(files []File) ([]File, bool, error) {
	out := append([]File(nil), files...)
	seen := make(map[string]struct{}, len(out))
	changed := false
	for i := range out {
		f := &out[i]
		if f.Reference != nil && validateReference(*f) != nil {
			return nil, false, ErrUnknownReference
		}
	}
	for i := range out {
		f := &out[i]
		if f.EntryID == "" || f.Revision == 0 {
			if err := assignIdentity(f); err != nil {
				return nil, false, err
			}
			changed = true
		}
		if _, exists := seen[f.EntryID]; exists {
			f.EntryID = ""
			if err := assignIdentity(f); err != nil {
				return nil, false, err
			}
			changed = true
		}
		seen[f.EntryID] = struct{}{}
		if f.Reference == nil && !f.Folder && f.Snap != "" {
			if err := assignReference(f); err != nil {
				return nil, false, err
			}
			changed = true
		}
		if err := validateReference(*f); err != nil {
			return nil, false, err
		}
	}
	return out, changed, nil
}

func assignIdentity(f *File) error {
	if f.EntryID == "" {
		id, err := newEntryID()
		if err != nil {
			return err
		}
		f.EntryID = id
	}
	if f.Revision == 0 {
		f.Revision = 1
	}
	return assignReference(f)
}

func assignReference(f *File) error {
	if f.Reference == nil && !f.Folder && f.Snap != "" {
		object := f.Object
		if object == "" {
			object = f.Hash
		}
		f.Reference = &Reference{Backend: ResticBackend, Version: 1, Snapshot: f.Snap, Object: object}
	}
	return validateReference(*f)
}

func validateReference(f File) error {
	if f.Reference == nil {
		return nil
	}
	ref := f.Reference
	if f.Folder || (f.Object != "" && ref.Object != f.Object) {
		return ErrUnknownReference
	}
	if ref.Backend == ResticBackend && ref.Version == 1 && ref.Snapshot == f.Snap {
		return nil
	}
	if ref.Backend == SharedBackend && ref.Version == 1 && ref.Snapshot == "" && ref.Object != "" && ref.Operation != "" && f.Snap == "" {
		return nil
	}
	return ErrUnknownReference
}

func ValidateFileReference(f File) error { return validateReference(f) }

func cloneFile(f File) File {
	if f.Reference != nil {
		ref := *f.Reference
		f.Reference = &ref
	}
	if f.DeletedAt != nil {
		deleted := *f.DeletedAt
		f.DeletedAt = &deleted
	}
	return f
}
