package sharedstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

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
	_, err := s.db.ExecContext(ctx, "UPDATE operations SET state='aborted' WHERE op_id=? AND state IN ('prepared','published')", op)
	if err == nil {
		_, err = s.db.ExecContext(ctx, "UPDATE owners SET state='aborted' WHERE op_id=? AND state IN ('prepared','published')", op)
	}
	return err
}

// Read requires the exact live owner/entry/revision tuple and an unlocked owner vault.
func (s *Store) Read(ctx context.Context, owner string, v *vault.Vault, ref Reference, dst io.Writer) error {
	if owner == "" || v == nil || !v.Unlocked() || ref.Version != formatVersion || !validObjectID(ref.ObjectID) || ref.EntryID == "" || ref.Revision == 0 {
		return ErrDenied
	}
	var objectID, op, state, opState string
	var wrapper, nodeWrapped []byte
	var size int64
	err := s.db.QueryRowContext(ctx, `SELECT o.object_id,o.op_id,o.state,p.state,o.wrapped_key,x.node_key,x.plain_len FROM owners o JOIN operations p ON p.op_id=o.op_id JOIN objects x ON x.object_id=o.object_id WHERE o.owner_id=? AND o.entry_id=? AND o.revision=?`, s.keys.ownerToken(owner), ref.EntryID, ref.Revision).Scan(&objectID, &op, &state, &opState, &wrapper, &nodeWrapped, &size)
	if err != nil {
		return ErrDenied
	}
	if state != "live" || opState != "committed" || objectID != ref.ObjectID || op != ref.Operation {
		return ErrDenied
	}
	plain, err := v.Unwrap(wrapper)
	if err != nil {
		return ErrDenied
	}
	defer cryptox.Zero(plain)
	var envelope struct {
		Version  int    `json:"version"`
		Owner    string `json:"owner"`
		Entry    string `json:"entry"`
		Revision uint64 `json:"revision"`
		Object   string `json:"object"`
		Key      string `json:"key"`
	}
	if err = json.Unmarshal(plain, &envelope); err != nil || envelope.Version != formatVersion || envelope.Owner != owner || envelope.Entry != ref.EntryID || envelope.Revision != ref.Revision || envelope.Object != objectID {
		return ErrDenied
	}
	key, err := cryptox.B64d(envelope.Key)
	if err != nil || len(key) != 32 {
		return ErrDenied
	}
	defer cryptox.Zero(key)
	nodeKey, err := s.unwrapNodeKey(nodeWrapped)
	if err != nil {
		return ErrState
	}
	defer cryptox.Zero(nodeKey)
	if !equalBytes(key, nodeKey) {
		return ErrState
	}
	file, err := os.Open(s.objectPath(objectID))
	if err != nil {
		return ErrState
	}
	defer file.Close()
	counted := &countWriter{w: contextWriter{ctx, dst}}
	if err = decryptFile(file, counted, objectID, key); err != nil {
		return err
	}
	if counted.n != size {
		return ErrState
	}
	return nil
}

// Release removes only one owner's reference. Shared payload collection is a later phase.
func (s *Store) Release(ctx context.Context, owner, entry string, revision uint64) error {
	token := s.keys.ownerToken(owner)
	_, err := s.db.ExecContext(ctx, "DELETE FROM owners WHERE owner_id=? AND entry_id=? AND revision=?", token, entry, revision)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE operations SET state='released' WHERE owner_id=? AND entry_id=? AND revision=? AND state='committed'", token, entry, revision)
	return err
}

func (s *Store) ObjectCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM objects WHERE state='ready'").Scan(&n)
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

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var n byte
	for i := range a {
		n |= a[i] ^ b[i]
	}
	return n == 0
}

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (w contextWriter) Write(p []byte) (int, error) {
	select {
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	default:
		return w.w.Write(p)
	}
}
