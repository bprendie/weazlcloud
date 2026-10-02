package catalog

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

type SourceMapping struct {
	DeviceID       string `json:"device_id"`
	Namespace      string `json:"namespace"`
	SourceID       string `json:"source_id"`
	Kind           string `json:"kind"`
	ServerID       string `json:"server_id"`
	SourceRevision string `json:"source_revision"`
	ServerRevision uint64 `json:"server_revision"`
}
type SourceOperation struct {
	OperationID      string   `json:"operation_id"`
	Namespace        string   `json:"namespace"`
	SourceID         string   `json:"source_id"`
	SourceRevision   string   `json:"source_revision"`
	Kind             string   `json:"kind"`
	ParentSourceID   string   `json:"parent_source_id,omitempty"`
	Title            string   `json:"title"`
	Position         int      `json:"position"`
	ExpectedRevision uint64   `json:"expected_revision"`
	Deleted          bool     `json:"deleted,omitempty"`
	AddIDs           []string `json:"add_ids,omitempty"`
}
type SourceReceipt struct {
	Digest   string `json:"digest"`
	ServerID string `json:"server_id"`
	Revision uint64 `json:"revision"`
}
type SourceOutcome struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	Code        string `json:"code,omitempty"`
	ServerID    string `json:"server_id,omitempty"`
	Revision    uint64 `json:"revision,omitempty"`
}

// SafeSourceKey hashes UTF-8 bytes with unsigned 64-bit big-endian lengths.
// It is an identity key, never a media hash; raw identifiers stay encrypted.
func SafeSourceKey(namespace, opaque string) string {
	h := sha256.New()
	for _, s := range []string{namespace, opaque} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func sourceKey(device, namespace, id string) string {
	return SafeSourceKey(device, SafeSourceKey(namespace, id))
}
func (c *Catalog) SourceMapping(device, namespace, id string) (SourceMapping, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.collections.Sources[sourceKey(device, namespace, id)]
	if !ok {
		return m, ErrNotFound
	}
	return m, nil
}

// Operations are individually atomic. Children may precede parents in a page.
// Failed operations do not consume their operation ID; a lost success is replayed.
func (c *Catalog) ImportSourceOperations(device string, ops []SourceOperation, memberships bool) ([]SourceOutcome, error) {
	if device == "" || len(device) > 100 || len(ops) > 200 || len(ops) == 0 {
		return nil, ErrAlbumInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]SourceOutcome, len(ops))
	pending := make(map[int]bool, len(ops))
	for i := range ops {
		pending[i] = true
	}
	for len(pending) > 0 {
		progress := false
		for i, op := range ops {
			if !pending[i] {
				continue
			}
			receipt, err := c.applySourceOperationLocked(device, op, memberships)
			if errors.Is(err, ErrSourceDependency) {
				continue
			}
			outcome := SourceOutcome{OperationID: op.OperationID, Status: "applied", ServerID: receipt.ServerID, Revision: receipt.Revision}
			if err != nil {
				outcome.Status = "invalid"
				outcome.Code = "invalid_specification"
				if !errors.Is(err, ErrAlbumInvalid) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrDescendant) && !errors.Is(err, ErrRevisionMismatch) && !errors.Is(err, ErrSourceConflict) && !errors.Is(err, ErrRevisionOverflow) {
					outcome.Status = "retryable"
					outcome.Code = "service_unavailable"
				}
				if errors.Is(err, ErrRevisionMismatch) {
					outcome.Status = "conflict"
					outcome.Code = "stale_revision"
				}
				if errors.Is(err, ErrSourceConflict) {
					outcome.Status = "conflict"
					outcome.Code = "idempotency_conflict"
				}
			}
			result[i] = outcome
			delete(pending, i)
			progress = true
		}
		if !progress {
			for i := range pending {
				result[i] = SourceOutcome{OperationID: ops[i].OperationID, Status: "dependency-blocked", Code: "missing_parent"}
			}
			break
		}
	}
	return result, nil
}

var ErrSourceDependency = errors.New("source parent not imported")

func (c *Catalog) applySourceOperationLocked(device string, op SourceOperation, memberships bool) (SourceReceipt, error) {
	if op.OperationID == "" || len(op.OperationID) > 200 || op.Namespace == "" || len(op.Namespace) > 200 || op.SourceID == "" || len(op.SourceID) > 4096 || op.SourceRevision == "" || len(op.SourceRevision) > 200 || len(op.ParentSourceID) > 4096 || len(op.AddIDs) > 200 {
		return SourceReceipt{}, ErrAlbumInvalid
	}
	raw, _ := json.Marshal(struct {
		Op      SourceOperation
		Members bool
	}{op, memberships})
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	key := sourceKey(device, op.Namespace, op.SourceID)
	operationKey := sourceKey(device, op.Namespace, op.OperationID)
	if old, ok := c.collections.Operations[operationKey]; ok {
		if old.Digest != digest {
			return old, ErrSourceConflict
		}
		return old, nil
	}
	m, exists := c.collections.Sources[key]
	if exists && (m.Kind != op.Kind || m.ServerRevision != op.ExpectedRevision) {
		return SourceReceipt{}, ErrRevisionMismatch
	}
	if !exists && op.ExpectedRevision != 0 {
		return SourceReceipt{}, ErrRevisionMismatch
	}
	parent := ""
	if op.ParentSourceID != "" {
		p, ok := c.collections.Sources[sourceKey(device, op.Namespace, op.ParentSourceID)]
		if !ok {
			return SourceReceipt{}, ErrSourceDependency
		}
		if p.Kind != "folder" {
			return SourceReceipt{}, ErrAlbumInvalid
		}
		parent = p.ServerID
	}
	oldState, oldAlbums := c.collections, c.albums
	c.collections = cloneCollectionState(oldState)
	c.albums = cloneAlbums(oldAlbums)
	rollback := func() { c.collections = oldState; c.albums = oldAlbums }
	var err error
	if op.Deleted { // One-way backup retains organization and bytes.
		if !exists {
			rollback()
			return SourceReceipt{}, ErrNotFound
		}
		err = c.checkSourceRevisionLocked(m)
	} else if memberships {
		if !exists || op.Kind != "album" {
			rollback()
			return SourceReceipt{}, ErrNotFound
		}
		err = c.importSourceMembersLocked(&m, op)
	} else if op.Kind == "folder" {
		f := CollectionFolder{ID: m.ServerID, Revision: op.ExpectedRevision, ParentID: parent, Title: op.Title, Position: op.Position}
		f, err = c.mutateCollectionFolderLocked(f)
		m.ServerID, m.ServerRevision = f.ID, f.Revision
	} else if op.Kind == "album" {
		err = c.importSourceAlbumLocked(&m, op, parent)
	} else {
		err = ErrAlbumInvalid
	}
	if err != nil {
		rollback()
		return SourceReceipt{}, err
	}
	m.DeviceID, m.Namespace, m.SourceID, m.Kind, m.SourceRevision = device, op.Namespace, op.SourceID, op.Kind, op.SourceRevision
	c.collections.Sources[key] = m
	receipt := SourceReceipt{Digest: digest, ServerID: m.ServerID, Revision: m.ServerRevision}
	c.collections.Operations[operationKey] = receipt
	if err := c.saveFilesLocked(c.files); err != nil {
		rollback()
		return SourceReceipt{}, err
	}
	return receipt, nil
}
func (c *Catalog) checkSourceRevisionLocked(m SourceMapping) error {
	for _, f := range c.collections.Folders {
		if f.ID == m.ServerID && f.Revision == m.ServerRevision {
			return nil
		}
	}
	for _, a := range c.albums {
		if a.ID == m.ServerID && a.Revision == m.ServerRevision {
			return nil
		}
	}
	return ErrRevisionMismatch
}
func (c *Catalog) importSourceAlbumLocked(m *SourceMapping, op SourceOperation, parent string) error {
	title := strings.TrimSpace(op.Title)
	if title == "" || len(title) > 200 || op.Position < 0 || op.Position > 100000 {
		return ErrAlbumInvalid
	}
	if err := c.validateCollectionParentLocked(parent, ""); err != nil {
		return err
	}
	for i, a := range c.albums {
		if a.ID == m.ServerID {
			if a.Revision != op.ExpectedRevision {
				return ErrRevisionMismatch
			}
			if a.Revision == ^uint64(0) {
				return ErrRevisionOverflow
			}
			a.Title, a.ParentID, a.Position = title, parent, op.Position
			a.Revision++
			c.albums[i] = a
			m.ServerRevision = a.Revision
			return nil
		}
	}
	if m.ServerID != "" {
		return ErrRevisionMismatch
	}
	id, err := newEntryID()
	if err != nil {
		return err
	}
	a := Album{ID: "pa_" + id, Revision: 1, Title: title, ParentID: parent, Position: op.Position, AssetIDs: []string{}}
	c.albums = append(c.albums, a)
	m.ServerID, m.ServerRevision = a.ID, 1
	return nil
}
func (c *Catalog) importSourceMembersLocked(m *SourceMapping, op SourceOperation) error {
	for i, a := range c.albums {
		if a.ID != m.ServerID {
			continue
		}
		if a.Revision != op.ExpectedRevision {
			return ErrRevisionMismatch
		}
		if a.Revision == ^uint64(0) {
			return ErrRevisionOverflow
		}
		seen := map[string]bool{}
		for _, id := range a.AssetIDs {
			seen[id] = true
		}
		valid := map[string]bool{}
		for _, f := range c.files {
			if photoAlbumMember(f) {
				valid[f.EntryID] = true
			}
		}
		for _, id := range op.AddIDs {
			if !valid[id] {
				return ErrNotFound
			}
			if !seen[id] {
				a.AssetIDs = append(a.AssetIDs, id)
				seen[id] = true
			}
		}
		if len(a.AssetIDs) > 100000 {
			return ErrAlbumInvalid
		}
		a.Revision++
		c.albums[i] = a
		m.ServerRevision = a.Revision
		return nil
	}
	return ErrRevisionMismatch
}
