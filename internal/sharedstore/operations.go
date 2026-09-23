package sharedstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Grant creates a separately authorized owner-entry wrapper for existing bytes.
func (s *Store) Grant(ctx context.Context, owner string, v *vault.Vault, source Reference, entry string, revision uint64) (Reference, error) {
	if entry == "" || revision == 0 {
		return Reference{}, ErrDenied
	}
	// Verify the source through the ordinary owner path before issuing another wrapper.
	if err := s.Read(ctx, owner, v, source, io.Discard); err != nil {
		return Reference{}, err
	}
	// Re-open only the short-lived source wrapper and rewrap its file key to the new identity.
	var wrapped []byte
	if err := s.db.QueryRowContext(ctx, "SELECT wrapped_key FROM owners WHERE owner_id=? AND entry_id=? AND revision=? AND object_id=? AND op_id=? AND state='live'", s.keys.ownerToken(owner), source.EntryID, source.Revision, source.ObjectID, source.Operation).Scan(&wrapped); err != nil {
		return Reference{}, ErrDenied
	}
	plain, err := v.Unwrap(wrapped)
	if err != nil {
		return Reference{}, ErrDenied
	}
	defer cryptox.Zero(plain)
	var env struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(plain, &env); err != nil {
		return Reference{}, ErrState
	}
	opRaw, err := cryptox.Random(16)
	if err != nil {
		return Reference{}, err
	}
	op := opaqueID(opRaw)
	copyEnv := map[string]any{"version": source.Version, "owner": owner, "entry": entry, "revision": revision, "object": source.ObjectID, "key": env.Key}
	newPlain, err := json.Marshal(copyEnv)
	if err != nil {
		return Reference{}, err
	}
	wrapper, err := v.Wrap(newPlain)
	cryptox.Zero(newPlain)
	if err != nil {
		return Reference{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Reference{}, err
	}
	defer tx.Rollback()
	token := s.keys.ownerToken(owner)
	if _, err = tx.ExecContext(ctx, "INSERT INTO operations(op_id,owner_id,entry_id,revision,object_id,state) VALUES(?,?,?,?,?,'committed')", op, token, entry, revision, source.ObjectID); err != nil {
		return Reference{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO owners(owner_id,entry_id,revision,object_id,wrapped_key,op_id,state) VALUES(?,?,?,?,?,?,'live')", token, entry, revision, source.ObjectID, wrapper, op); err != nil {
		return Reference{}, err
	}
	if err = tx.Commit(); err != nil {
		return Reference{}, err
	}
	return Reference{Version: source.Version, ObjectID: source.ObjectID, EntryID: entry, Revision: revision, Operation: op}, nil
}

// MarkPublished records that the caller's encrypted owner catalog contains this operation.
func (s *Store) MarkPublished(ctx context.Context, op string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE operations SET state='published' WHERE op_id=? AND state IN ('prepared','published','committed')", op)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrState
	}
	_, err = s.db.ExecContext(ctx, "UPDATE owners SET state='published' WHERE op_id=? AND state='prepared'", op)
	if err == nil {
		err = fail(s.options, "published")
	}
	return err
}

// Commit is idempotent and makes a catalog-published owner reference readable.
func (s *Store) Commit(ctx context.Context, op string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	if err = tx.QueryRowContext(ctx, "SELECT state FROM operations WHERE op_id=?", op).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return ErrState
	} else if err != nil {
		return err
	}
	if state == "committed" {
		return nil
	}
	if state != "published" {
		return ErrState
	}
	var newer int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM owners WHERE owner_id=(SELECT owner_id FROM operations WHERE op_id=?) AND entry_id=(SELECT entry_id FROM operations WHERE op_id=?) AND revision>(SELECT revision FROM operations WHERE op_id=?) AND state='live'`, op, op, op).Scan(&newer); err != nil {
		return err
	}
	if newer != 0 {
		return ErrStale
	}
	if _, err = tx.ExecContext(ctx, "UPDATE owners SET state='live' WHERE op_id=? AND state='published'", op); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE operations SET state='committed' WHERE op_id=? AND state='published'", op); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return fail(s.options, "committed")
}

// Recover resolves a durable operation after checking the caller's encrypted catalog.
// published must come from catalog inspection, never from a client request.
func (s *Store) Recover(ctx context.Context, op string, published bool) error {
	var state string
	if err := s.db.QueryRowContext(ctx, "SELECT state FROM operations WHERE op_id=?", op).Scan(&state); err != nil {
		return err
	}
	if state == "committed" {
		return nil
	}
	if published {
		if err := s.MarkPublished(ctx, op); err != nil {
			return err
		}
		return s.Commit(ctx, op)
	}
	if state != "prepared" && state != "published" {
		return ErrState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE operations SET state='aborted' WHERE op_id=? AND state IN ('prepared','published')", op); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM owners WHERE op_id=? AND state IN ('prepared','published')", op); err != nil {
		return err
	}
	return tx.Commit()
}

// Release removes only one owner's reference. Shared payload collection is a later phase.
func (s *Store) Release(ctx context.Context, owner, entry string, revision uint64) error {
	token := s.keys.ownerToken(owner)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE owners SET state='retired' WHERE owner_id=? AND entry_id=? AND revision=? AND EXISTS(SELECT 1 FROM holds h WHERE h.owner_id=owners.owner_id AND h.entry_id=owners.entry_id AND h.revision=owners.revision)", token, entry, revision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM owners WHERE owner_id=? AND entry_id=? AND revision=? AND state!='retired'", token, entry, revision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE operations SET state='released' WHERE owner_id=? AND entry_id=? AND revision=? AND state='committed'", token, entry, revision); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ObjectCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM objects x WHERE x.state='ready' AND ((x.kind='chunk' AND EXISTS(SELECT 1 FROM object_dependencies d JOIN objects p ON p.object_id=d.parent_id JOIN owners o ON o.object_id=p.object_id WHERE d.child_id=x.object_id AND p.state='ready' AND o.state='live')) OR (x.kind='whole' AND EXISTS(SELECT 1 FROM owners o WHERE o.object_id=x.object_id AND o.state='live')))`).Scan(&n)
	return n, err
}

func (s *Store) Pending(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT op_id FROM operations WHERE state IN ('prepared','published') ORDER BY op_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
