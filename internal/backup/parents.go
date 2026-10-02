package backup

import (
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/library"
)

// Independent intents may create the same initially absent relative parent.
// Reuse it only when another operation of this same source owns its exact current
// identity/revision/path. An unrelated or user-modified parent remains a conflict.
func adoptParents(tx *library.BackupTransaction, src sourceRecord, op *operation) error {
	files := make([]catalog.File, 0, len(op.Plan.Files))
	absent := make(map[string]bool)
	for _, p := range op.Plan.Absent {
		absent[p] = true
	}
	for _, f := range op.Plan.Files {
		if !f.Folder || f.EntryID == op.PrimaryID || f.Revision != 1 {
			files = append(files, f)
			continue
		}
		live, exists := tx.Catalog().Get(f.Path)
		if !exists {
			files = append(files, f)
			continue
		}
		rel := strings.TrimPrefix(f.Path, src.Destination.Path+"/")
		owned, ok := src.Items["\x00folder:"+rel]
		if !ok || !live.Folder || live.EntryID != owned.EntryID || live.Revision != owned.Revision || live.Path != owned.Path {
			return ErrStale
		}
		op.Plan.Guards = append(op.Plan.Guards, live)
		delete(absent, f.Path)
	}
	op.Plan.Files = files
	op.Plan.Absent = nil
	for p := range absent {
		op.Plan.Absent = append(op.Plan.Absent, p)
	}
	return nil
}
