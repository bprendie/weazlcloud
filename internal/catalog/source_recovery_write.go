package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// SourceRecoveryAdoption binds an existing owner collection to a new source
// identity. The selected old mapping and target are both checked at commit.
type SourceRecoveryAdoption struct {
	OperationID            string `json:"operation_id"`
	FromDeviceID           string `json:"from_device_id"`
	FromNamespace          string `json:"from_namespace"`
	FromSourceID           string `json:"from_source_id"`
	FromSourceRevision     string `json:"from_source_revision"`
	FromServerRevision     uint64 `json:"from_server_revision"`
	Namespace              string `json:"namespace"`
	SourceID               string `json:"source_id"`
	ServerID               string `json:"server_id"`
	ExpectedServerRevision uint64 `json:"expected_server_revision"`
}

func validSourceRecoveryAdoption(device string, a SourceRecoveryAdoption) bool {
	return device != "" && len(device) <= 100 && a.OperationID != "" && len(a.OperationID) <= 200 &&
		a.FromDeviceID != "" && len(a.FromDeviceID) <= 100 &&
		a.FromNamespace != "" && len(a.FromNamespace) <= 200 &&
		a.FromSourceID != "" && len(a.FromSourceID) <= 4096 &&
		a.FromSourceRevision != "" && len(a.FromSourceRevision) <= 200 &&
		a.Namespace != "" && len(a.Namespace) <= 200 &&
		a.SourceID != "" && len(a.SourceID) <= 4096 &&
		a.ServerID != "" && len(a.ServerID) <= 128 && a.FromServerRevision != 0 && a.ExpectedServerRevision != 0 &&
		!strings.ContainsRune(a.OperationID+a.FromDeviceID+a.FromNamespace+a.FromSourceID+a.FromSourceRevision+a.Namespace+a.SourceID+a.ServerID, 0) &&
		utf8.ValidString(a.OperationID) && utf8.ValidString(a.FromDeviceID) && utf8.ValidString(a.FromNamespace) &&
		utf8.ValidString(a.FromSourceID) && utf8.ValidString(a.FromSourceRevision) &&
		utf8.ValidString(a.Namespace) && utf8.ValidString(a.SourceID) && utf8.ValidString(a.ServerID)
}

// AdoptSourceCollection only adds the destination source mapping and an
// idempotency receipt. Existing target metadata, hierarchy and memberships are
// untouched. The caller must hold its publication authorization barrier.
func (c *Catalog) AdoptSourceCollection(device string, a SourceRecoveryAdoption) (SourceMapping, error) {
	if !validSourceRecoveryAdoption(device, a) {
		return SourceMapping{}, ErrAlbumInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fromKey := sourceKey(a.FromDeviceID, a.FromNamespace, a.FromSourceID)
	toKey := sourceKey(device, a.Namespace, a.SourceID)
	opKey := sourceKey(device, a.Namespace, a.OperationID)
	encoded, _ := json.Marshal(struct {
		Kind     string
		Device   string
		Adoption SourceRecoveryAdoption
	}{"collection-recovery-v1", device, a})
	digest := sha256.Sum256(encoded)
	clear(encoded)
	if receipt, exists := c.collections.Operations[opKey]; exists {
		if receipt.Digest != hex.EncodeToString(digest[:]) {
			return SourceMapping{}, ErrSourceConflict
		}
		m, ok := c.collections.Sources[toKey]
		if !ok || m.ServerID != receipt.ServerID {
			return SourceMapping{}, ErrSourceConflict
		}
		if _, alive := c.sourceCollectionRevisionLocked(m.Kind, m.ServerID); !alive {
			return SourceMapping{}, ErrRevisionMismatch
		}
		return m, nil
	}
	old, ok := c.collections.Sources[fromKey]
	if !ok || old.Kind != "album" && old.Kind != "folder" {
		return SourceMapping{}, ErrNotFound
	}
	if old.ServerID != a.ServerID || old.ServerRevision != a.FromServerRevision || old.SourceRevision != a.FromSourceRevision {
		return SourceMapping{}, ErrRevisionMismatch
	}
	current, exists := c.sourceCollectionRevisionLocked(old.Kind, old.ServerID)
	if !exists || current != a.ExpectedServerRevision {
		return SourceMapping{}, ErrRevisionMismatch
	}
	if _, exists := c.collections.Sources[toKey]; exists {
		// A prior mapping is never replaced by recovery, even if its target
		// currently matches: it may represent newer source work.
		return SourceMapping{}, ErrSourceConflict
	}
	m := SourceMapping{DeviceID: device, Namespace: a.Namespace, SourceID: a.SourceID,
		Kind: old.Kind, ServerID: old.ServerID, SourceRevision: old.SourceRevision,
		ServerRevision: current}
	previous := c.collections
	c.collections = cloneCollectionState(previous)
	c.collections.Sources[toKey] = m
	c.collections.Operations[opKey] = SourceReceipt{Digest: hex.EncodeToString(digest[:]), ServerID: m.ServerID, Revision: m.ServerRevision}
	if err := c.saveFilesLocked(c.files); err != nil {
		c.collections = previous
		return SourceMapping{}, err
	}
	return m, nil
}

func (c *Catalog) sourceCollectionRevisionLocked(kind, id string) (uint64, bool) {
	if kind == "album" {
		for _, a := range c.albums {
			if a.ID == id {
				return a.Revision, true
			}
		}
	} else if kind == "folder" {
		for _, f := range c.collections.Folders {
			if f.ID == id {
				return f.Revision, true
			}
		}
	}
	return 0, false
}
