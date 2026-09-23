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

// Read requires the exact live owner/entry/revision tuple and an unlocked owner vault.
func (s *Store) Read(ctx context.Context, owner string, v *vault.Vault, ref Reference, dst io.Writer) error {
	if owner == "" || v == nil || !v.Unlocked() || (ref.Version != formatVersion && ref.Version != chunkFormatVersion) || !validObjectID(ref.ObjectID) || ref.EntryID == "" || ref.Revision == 0 {
		return ErrDenied
	}
	var objectID, op, state, opState, objectKind string
	var wrapper, nodeWrapped []byte
	var size int64
	err := s.db.QueryRowContext(ctx, `SELECT o.object_id,o.op_id,o.state,p.state,o.wrapped_key,x.node_key,x.plain_len,x.kind FROM owners o JOIN operations p ON p.op_id=o.op_id JOIN objects x ON x.object_id=o.object_id WHERE o.owner_id=? AND o.entry_id=? AND o.revision=? AND (o.state='live' OR (o.state='retired' AND EXISTS(SELECT 1 FROM holds h WHERE h.owner_id=o.owner_id AND h.entry_id=o.entry_id AND h.revision=o.revision AND h.operation=o.op_id)))`, s.keys.ownerToken(owner), ref.EntryID, ref.Revision).Scan(&objectID, &op, &state, &opState, &wrapper, &nodeWrapped, &size, &objectKind)
	if err != nil {
		return ErrDenied
	}
	if !((state == "live" && opState == "committed") || (state == "retired" && opState == "released")) || objectID != ref.ObjectID || op != ref.Operation {
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
	if err = json.Unmarshal(plain, &envelope); err != nil || envelope.Version != ref.Version || envelope.Owner != owner || envelope.Entry != ref.EntryID || envelope.Revision != ref.Revision || envelope.Object != objectID {
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
	if ref.Version == chunkFormatVersion && objectKind == "manifest" {
		return s.readManifest(ctx, objectID, key, size, dst)
	}
	if ref.Version != formatVersion || objectKind != "whole" {
		return ErrFormat
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
