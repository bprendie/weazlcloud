package sharedstore

import (
	"context"
	"database/sql"
	"errors"
)

// clearAbortedRevision releases the unique owner/revision slot before a fresh
// random operation retries a migration that recovery safely aborted.
func (s *Store) clearAbortedRevision(ctx context.Context, owner, entry string, revision uint64) error {
	token := s.keys.ownerToken(owner)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var op, state string
	err = tx.QueryRowContext(ctx, "SELECT op_id,state FROM operations WHERE owner_id=? AND entry_id=? AND revision=?", token, entry, revision).Scan(&op, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if state != "aborted" {
		return nil
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM owners WHERE owner_id=? AND entry_id=? AND revision=? AND op_id=? AND state='aborted'", token, entry, revision, op); err != nil {
		return err
	}
	var owners int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM owners WHERE owner_id=? AND entry_id=? AND revision=?", token, entry, revision).Scan(&owners); err != nil {
		return err
	}
	if owners != 0 {
		return ErrState
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM operations WHERE op_id=? AND state='aborted'", op); err != nil {
		return err
	}
	return tx.Commit()
}
