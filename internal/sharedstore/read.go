package sharedstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"os"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Read requires the exact live owner, entry, and revision tuple.
func (s *Store) Read(ctx context.Context, owner string, v *vault.Vault, ref Reference, dst io.Writer) error {
	return s.read(ctx, owner, v, ref, dst, false)
}

// VerifyPrepared checks the exact private operation before catalog publication.
func (s *Store) VerifyPrepared(ctx context.Context, owner string, v *vault.Vault, prepared Prepared, expectedSize int64, expectedHash []byte) error {
	if expectedSize < 0 || len(expectedHash) != sha256.Size || prepared.Operation == "" || prepared.Reference.Operation != prepared.Operation {
		return ErrDenied
	}
	h := sha256.New()
	counted := &countWriter{w: h}
	if err := s.read(ctx, owner, v, prepared.Reference, counted, true); err != nil {
		return err
	}
	if counted.n != expectedSize || !equalBytes(h.Sum(nil), expectedHash) {
		return ErrState
	}
	return nil
}

func (s *Store) read(ctx context.Context, owner string, v *vault.Vault, ref Reference, dst io.Writer, prepared bool) error {
	if owner == "" || v == nil || !v.Unlocked() || (ref.Version != formatVersion && ref.Version != chunkFormatVersion) || !validObjectID(ref.ObjectID) || ref.EntryID == "" || ref.Revision == 0 {
		return ErrDenied
	}
	var objectID, op, state, opState, objectKind string
	var wrapper, nodeWrapped []byte
	var size int64
	err := s.db.QueryRowContext(ctx, `SELECT o.object_id,o.op_id,o.state,p.state,o.wrapped_key,x.node_key,x.plain_len,x.kind FROM owners o JOIN operations p ON p.op_id=o.op_id JOIN objects x ON x.object_id=o.object_id WHERE o.owner_id=? AND o.entry_id=? AND o.revision=? AND ((o.state='live' AND p.state='committed') OR (o.state='retired' AND EXISTS(SELECT 1 FROM holds h WHERE h.owner_id=o.owner_id AND h.entry_id=o.entry_id AND h.revision=o.revision AND h.operation=o.op_id)) OR (?=1 AND o.state='prepared' AND p.state='prepared' AND o.op_id=?))`, s.keys.ownerToken(owner), ref.EntryID, ref.Revision, prepared, ref.Operation).Scan(&objectID, &op, &state, &opState, &wrapper, &nodeWrapped, &size, &objectKind)
	if err != nil {
		return ErrDenied
	}
	validState := (state == "live" && opState == "committed") || (state == "retired" && opState == "released") || (prepared && state == "prepared" && opState == "prepared" && op == ref.Operation)
	if !validState || objectID != ref.ObjectID || op != ref.Operation {
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
