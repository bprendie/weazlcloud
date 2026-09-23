package sharedstore

import (
	"context"
	"errors"
	"time"
)

var migrationStates = map[string]bool{
	"discovered": true, "source-pinned": true, "copied": true,
	"destination-verified": true, "catalog-switched": true,
	"complete": true, "blocked": true,
}

type MigrationTally struct {
	State string `json:"state"`
	Items int64  `json:"items"`
	Bytes int64  `json:"bytes"`
}

// RecordMigration stores progress without exposing names or raw account IDs.
func (s *Store) RecordMigration(ctx context.Context, owner, entry string, revision uint64, state string, bytes int64, category string) error {
	if owner == "" || entry == "" || revision == 0 || !migrationStates[state] || bytes < 0 || len(category) > 48 {
		return errors.New("invalid migration progress")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO migration_items(owner_id,entry_id,revision,state,copied_bytes,error_category,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(owner_id,entry_id,revision) DO UPDATE SET state=excluded.state,copied_bytes=excluded.copied_bytes,error_category=excluded.error_category,updated_at=excluded.updated_at`, s.keys.ownerToken(owner), entry, revision, state, bytes, category, time.Now().UTC().Unix())
	return err
}

func (s *Store) MigrationStatus(ctx context.Context) ([]MigrationTally, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT state,count(*),coalesce(sum(copied_bytes),0) FROM migration_items GROUP BY state ORDER BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MigrationTally
	for rows.Next() {
		var item MigrationTally
		if err = rows.Scan(&item.State, &item.Items, &item.Bytes); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// FinishMigration reconciles a catalog switch that survived without its final
// progress update. It does not create records for ordinary shared uploads.
func (s *Store) FinishMigration(ctx context.Context, owner, entry string, sourceRevision uint64, bytes int64) error {
	if owner == "" || entry == "" || sourceRevision == 0 || bytes < 0 {
		return errors.New("invalid migration completion")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE migration_items SET state='complete',copied_bytes=?,error_category='',updated_at=? WHERE owner_id=? AND entry_id=? AND revision=? AND state IN ('destination-verified','catalog-switched')`, bytes, time.Now().UTC().Unix(), s.keys.ownerToken(owner), entry, sourceRevision)
	return err
}
