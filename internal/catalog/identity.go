package catalog

import (
	"encoding/hex"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func upgradeFiles(files []File) ([]File, bool, error) {
	out := append([]File(nil), files...)
	seen := make(map[string]struct{}, len(out))
	changed := false
	for i := range out {
		f := &out[i]
		if f.Reference != nil && (f.Reference.Backend != ResticBackend || f.Reference.Version != 1) {
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
		id, err := cryptox.Random(16)
		if err != nil {
			return err
		}
		f.EntryID = hex.EncodeToString(id)
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
	if f.Folder || ref.Backend != ResticBackend || ref.Version != 1 || ref.Snapshot != f.Snap || (f.Object != "" && ref.Object != f.Object) {
		return ErrUnknownReference
	}
	return nil
}

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
