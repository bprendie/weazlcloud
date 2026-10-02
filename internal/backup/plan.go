package backup

import (
	"path"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

func planItem(tx *library.BackupTransaction, src sourceRecord, spec Spec) (catalog.BackupMutation, string, error) {
	var plan catalog.BackupMutation
	current := tx.Catalog().List()
	byPath := make(map[string]catalog.File)
	byID := make(map[string]catalog.File)
	for _, f := range current {
		byPath[f.Path] = f
		byID[f.EntryID] = f
	}
	dest, ok := byID[src.Destination.EntryID]
	if !ok || dest.Path != src.Destination.Path || dest.Revision != src.Destination.Revision || !dest.Folder {
		return plan, "", ErrStale
	}
	target := dest.Path + "/" + spec.RelativePath
	// Guard all ancestors, including parents above the selected destination.
	for p := path.Dir(target); p != "."; p = path.Dir(p) {
		if f, ok := byPath[p]; ok {
			if !f.Folder {
				return plan, "", catalog.ErrConflict
			}
			plan.Guards = append(plan.Guards, f)
			if owned, exists := src.Items["\x00folder:"+strings.TrimPrefix(p, dest.Path+"/")]; exists && (owned.EntryID != f.EntryID || owned.Revision != f.Revision || owned.Path != f.Path) {
				return plan, "", ErrStale
			}
		} else {
			id, err := catalog.NewBackupIdentity()
			if err != nil {
				return plan, "", err
			}
			plan.Absent = append(plan.Absent, p)
			plan.Files = append(plan.Files, catalog.File{EntryID: id, Revision: 1, Path: p, Folder: true, Present: true, Mtime: time.Now().UTC(), ImportedAt: time.Now().UTC()})
		}
	}
	old, mapped := src.Items[spec.ItemID]
	var f catalog.File
	if mapped {
		live, ok := byID[old.EntryID]
		if !ok || live.Path != old.Path || live.Revision != old.Revision || live.Folder != old.Folder || spec.ExpectedEntryID != old.EntryID || spec.ExpectedRevision != old.Revision || old.Revision == ^uint64(0) {
			return plan, "", ErrStale
		}
		if old.Folder != (spec.Kind == "folder") {
			return plan, "", ErrInvalid
		}
		plan.Guards = append(plan.Guards, live)
		f = live
		f.Path = target
		f.Revision++
		if old.Folder && old.Path != target {
			if strings.HasPrefix(target, old.Path+"/") {
				return plan, "", catalog.ErrDescendant
			}
			for _, child := range current {
				if !strings.HasPrefix(child.Path, old.Path+"/") {
					continue
				}
				owned := false
				for _, mappedChild := range src.Items {
					if mappedChild.EntryID == child.EntryID && mappedChild.Revision == child.Revision && mappedChild.Path == child.Path {
						owned = true
						break
					}
				}
				if !owned || child.Revision == ^uint64(0) {
					return plan, "", ErrStale
				}
				plan.Guards = append(plan.Guards, child)
				moved := child
				moved.Path = target + strings.TrimPrefix(child.Path, old.Path)
				moved.Revision++
				if collision, ok := byPath[moved.Path]; ok && collision.EntryID != child.EntryID {
					return plan, "", catalog.ErrConflict
				}
				plan.Absent = append(plan.Absent, moved.Path)
				plan.Files = append(plan.Files, moved)
			}
		}
	} else {
		if spec.ExpectedEntryID != "" {
			return plan, "", ErrStale
		}
		id, err := catalog.NewBackupIdentity()
		if err != nil {
			return plan, "", err
		}
		f = catalog.File{EntryID: id, Revision: 1, Present: true, Path: target, Folder: spec.Kind == "folder", ImportedAt: time.Now().UTC()}
	}
	if occupant, ok := byPath[target]; ok && occupant.EntryID != f.EntryID {
		return plan, "", catalog.ErrConflict
	}
	if !mapped || old.Path != target {
		plan.Absent = append(plan.Absent, target)
	}
	f.Size = spec.Size
	f.Hash = spec.SHA256
	f.Mtime = spec.Mtime
	if f.Mtime.IsZero() {
		f.Mtime = time.Now().UTC()
	}
	if !f.Folder {
		f.Reference = nil
		f.Snap = ""
		f.Object = ""
	}
	plan.Files = append(plan.Files, f)
	return plan, f.EntryID, tx.Catalog().CheckBackup(plan)
}
