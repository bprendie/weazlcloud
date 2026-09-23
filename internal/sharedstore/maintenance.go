package sharedstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type Stats struct {
	Objects        int64
	UniqueBytes    int64
	LogicalBytes   int64
	AllocatedBytes int64
}

// Hold pins one authorized live owner reference until the returned release is called.
func (s *Store) Hold(ctx context.Context, owner string, ref Reference) (func(), error) {
	if !validObjectID(ref.ObjectID) || !validOperationID(ref.Operation) {
		return nil, ErrDenied
	}
	raw, err := cryptox.Random(16)
	if err != nil {
		return nil, err
	}
	holdID := opaqueID(raw)
	token := s.keys.ownerToken(owner)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO holds(hold_id,object_id,owner_id,entry_id,revision,operation,created_at) SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM owners WHERE owner_id=? AND entry_id=? AND revision=? AND object_id=? AND op_id=? AND state='live')`, holdID, ref.ObjectID, token, ref.EntryID, ref.Revision, ref.Operation, time.Now().Unix(), token, ref.EntryID, ref.Revision, ref.ObjectID, ref.Operation)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		_ = tx.Rollback()
		if err != nil {
			return nil, err
		}
		return nil, ErrDenied
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return func() {
		tx, e := s.db.Begin()
		if e != nil {
			return
		}
		defer tx.Rollback()
		if _, e = tx.Exec("DELETE FROM holds WHERE hold_id=?", holdID); e != nil {
			return
		}
		if _, e = tx.Exec(`DELETE FROM owners WHERE owner_id=? AND entry_id=? AND revision=? AND state='retired' AND NOT EXISTS(SELECT 1 FROM holds WHERE owner_id=? AND entry_id=? AND revision=?)`, token, ref.EntryID, ref.Revision, token, ref.EntryID, ref.Revision); e != nil {
			return
		}
		_ = tx.Commit()
	}, nil
}

// ReconcileOwner resolves interrupted writes and stale references only after
// the owner catalog has been authenticated and inspected by the caller.
func (s *Store) ReconcileOwner(ctx context.Context, owner string, published map[string]struct{}) error {
	rows, err := s.db.QueryContext(ctx, "SELECT op_id FROM operations WHERE owner_id=? AND state IN ('prepared','published') ORDER BY op_id", s.keys.ownerToken(owner))
	if err != nil {
		return err
	}
	var pending []string
	for rows.Next() {
		var op string
		if err = rows.Scan(&op); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, op)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, op := range pending {
		_, committed := published[op]
		if err = s.Recover(ctx, op, committed); err != nil {
			return err
		}
	}
	rows, err = s.db.QueryContext(ctx, "SELECT entry_id,revision,op_id FROM owners WHERE owner_id=? AND state='live'", s.keys.ownerToken(owner))
	if err != nil {
		return err
	}
	type staleReference struct {
		entry, operation string
		revision         uint64
	}
	var stale []staleReference
	for rows.Next() {
		var ref staleReference
		if err = rows.Scan(&ref.entry, &ref.revision, &ref.operation); err != nil {
			rows.Close()
			return err
		}
		if _, ok := published[ref.operation]; !ok {
			stale = append(stale, ref)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, ref := range stale {
		if err = s.Release(ctx, owner, ref.entry, ref.revision); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseOwner removes every reference and pending operation for an account.
func (s *Store) ReleaseOwner(ctx context.Context, owner string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	token := s.keys.ownerToken(owner)
	if _, err = tx.ExecContext(ctx, "UPDATE owners SET state='retired' WHERE owner_id=? AND EXISTS(SELECT 1 FROM holds h WHERE h.owner_id=owners.owner_id AND h.entry_id=owners.entry_id AND h.revision=owners.revision)", token); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM owners WHERE owner_id=? AND state!='retired'", token); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM operations WHERE owner_id=?", token); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearAbandonedHolds is called once after process startup, when in-memory archive jobs no longer exist.
func (s *Store) ClearAbandonedHolds(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM holds"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM owners WHERE state='retired'"); err != nil {
		return err
	}
	return tx.Commit()
}

// Collect atomically claims only objects with no owner refs or holds, then removes bytes.
func (s *Store) Collect(ctx context.Context) (int64, error) {
	var freed int64
	for {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return freed, err
		}
		var id string
		err = tx.QueryRowContext(ctx, `SELECT x.object_id FROM objects x WHERE x.state='ready' AND NOT EXISTS(SELECT 1 FROM owners o WHERE o.object_id=x.object_id AND o.state IN ('prepared','published','live')) AND NOT EXISTS(SELECT 1 FROM holds h WHERE h.object_id=x.object_id) LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return freed, nil
		}
		if err != nil {
			_ = tx.Rollback()
			return freed, err
		}
		res, err := tx.ExecContext(ctx, "UPDATE objects SET state='deleting' WHERE object_id=? AND state='ready'", id)
		if err != nil {
			_ = tx.Rollback()
			return freed, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return freed, err
		}
		if n == 0 {
			_ = tx.Rollback()
			continue
		}
		if err = tx.Commit(); err != nil {
			return freed, err
		}
		path := s.objectPath(id)
		info, e := os.Stat(path)
		if e == nil {
			freed += info.Size()
		}
		if e != nil && !os.IsNotExist(e) {
			return freed, e
		}
		if e = os.Remove(path); e != nil && !os.IsNotExist(e) {
			return freed, e
		}
		if e = syncDir(filepath.Dir(path)); e != nil {
			return freed, e
		}
		if _, e = s.db.ExecContext(ctx, "DELETE FROM objects WHERE object_id=? AND state='deleting'", id); e != nil {
			return freed, e
		}
	}
}

func (s *Store) resumeDeletes() error {
	rows, err := s.db.Query("SELECT object_id FROM objects WHERE state='deleting'")
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !validObjectID(id) {
			rows.Close()
			return ErrState
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		path := s.objectPath(id)
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err = syncDir(filepath.Dir(path)); err != nil {
			return err
		}
		if _, err = s.db.Exec("DELETE FROM objects WHERE object_id=? AND state='deleting'", id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Metrics(ctx context.Context) (Stats, error) {
	var out Stats
	err := s.db.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(x.plain_len),0),coalesce(sum(x.plain_len*(SELECT count(*) FROM owners o WHERE o.object_id=x.object_id AND o.state='live')),0) FROM objects x WHERE x.state='ready' AND EXISTS(SELECT 1 FROM owners o WHERE o.object_id=x.object_id AND o.state='live')`).Scan(&out.Objects, &out.UniqueBytes, &out.LogicalBytes)
	if err != nil {
		return out, err
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "shared-objects"))
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".wobj") || !validObjectID(strings.TrimSuffix(entry.Name(), ".wobj")) {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return out, e
		}
		out.AllocatedBytes += info.Size()
	}
	return out, nil
}
