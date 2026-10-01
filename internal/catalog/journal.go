package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

const ChangeRetention = 8192

var ErrSyncExpired = errors.New("photo sync checkpoint expired; resync required")

type SyncPosition struct {
	Epoch    string `json:"epoch"`
	Sequence uint64 `json:"sequence"`
	Hash     string `json:"hash"`
}

type ChangeRecord struct {
	Sequence uint64 `json:"sequence"`
	Hash     string `json:"hash"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Deleted  bool   `json:"deleted,omitempty"`
	Photos   bool   `json:"photos_scope,omitempty"`
	File     *File  `json:"file,omitempty"`
	Album    *Album `json:"album,omitempty"`
}

type Journal struct {
	SyncPosition
	Records []ChangeRecord `json:"records,omitempty"`
}

// Journal and metadata publish in the same encrypted atomic catalog write.
// A hash chain distinguishes a restored/branched checkpoint from a current one.
func (c *Catalog) nextJournalLocked(files []File) (Journal, error) {
	journal := cloneJournal(c.journal)
	if journal.Epoch == "" {
		id, err := newEntryID()
		if err != nil {
			return Journal{}, err
		}
		journal.Epoch = id
	}
	previous := make(map[string]File, len(c.files))
	for _, file := range c.files {
		previous[file.EntryID] = file
	}
	for _, file := range files {
		old, exists := previous[file.EntryID]
		delete(previous, file.EntryID)
		if exists && old.Revision == file.Revision && old.Path == file.Path && old.Present == file.Present {
			continue
		}
		kind := "asset"
		if file.Folder {
			kind = "folder"
		}
		copied := cloneFile(file)
		if err := journal.append(ChangeRecord{Kind: kind, ID: file.EntryID, File: &copied, Deleted: !file.Present, Photos: syncPhotoPath(file.Path) || exists && syncPhotoPath(old.Path)}); err != nil {
			return Journal{}, err
		}
	}
	for id, file := range previous {
		kind := "asset"
		if file.Folder {
			kind = "folder"
		}
		if err := journal.append(ChangeRecord{Kind: kind, ID: id, Deleted: true, Photos: syncPhotoPath(file.Path)}); err != nil {
			return Journal{}, err
		}
	}
	oldAlbums := make(map[string]Album, len(c.savedAlbums))
	for _, album := range c.savedAlbums {
		oldAlbums[album.ID] = album
	}
	for _, album := range c.albums {
		old, exists := oldAlbums[album.ID]
		delete(oldAlbums, album.ID)
		if exists && reflect.DeepEqual(old, album) {
			continue
		}
		copied := cloneAlbum(album)
		if err := journal.append(ChangeRecord{Kind: "album", ID: album.ID, Album: &copied, Photos: true}); err != nil {
			return Journal{}, err
		}
	}
	for id := range oldAlbums {
		if err := journal.append(ChangeRecord{Kind: "album", ID: id, Deleted: true, Photos: true}); err != nil {
			return Journal{}, err
		}
	}
	if len(journal.Records) > ChangeRetention {
		journal.Records = append([]ChangeRecord(nil), journal.Records[len(journal.Records)-ChangeRetention:]...)
	}
	return journal, nil
}

func syncPhotoPath(path string) bool { return path == "Photos" || strings.HasPrefix(path, "Photos/") }

func (journal *Journal) append(record ChangeRecord) error {
	if journal.Sequence == ^uint64(0) {
		return ErrRevisionOverflow
	}
	record.Sequence = journal.Sequence + 1
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	hash := sha256.New()
	hash.Write([]byte(journal.Hash))
	hash.Write(raw)
	record.Hash = hex.EncodeToString(hash.Sum(nil))
	journal.Sequence, journal.Hash = record.Sequence, record.Hash
	journal.Records = append(journal.Records, record)
	return nil
}

func cloneJournal(journal Journal) Journal {
	journal.Records = append([]ChangeRecord(nil), journal.Records...)
	for i := range journal.Records {
		record := &journal.Records[i]
		if record.File != nil {
			file := cloneFile(*record.File)
			record.File = &file
		}
		if record.Album != nil {
			album := cloneAlbum(*record.Album)
			record.Album = &album
		}
	}
	return journal
}

func (c *Catalog) SyncPosition() (SyncPosition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.journal.Epoch == "" {
		if err := c.saveFilesLocked(c.files); err != nil {
			return SyncPosition{}, err
		}
	}
	return c.journal.SyncPosition, nil
}

func (c *Catalog) ValidateSync(position SyncPosition) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.validateSyncLocked(position)
}

func (c *Catalog) validateSyncLocked(position SyncPosition) error {
	if position.Epoch != c.journal.Epoch || position.Sequence > c.journal.Sequence {
		return ErrSyncExpired
	}
	if position.Sequence == c.journal.Sequence && position.Hash == c.journal.Hash {
		return nil
	}
	if position.Sequence == 0 && position.Hash == "" && (len(c.journal.Records) == 0 || c.journal.Records[0].Sequence == 1) {
		return nil
	}
	for _, record := range c.journal.Records {
		if record.Sequence == position.Sequence && record.Hash == position.Hash {
			return nil
		}
	}
	return ErrSyncExpired
}

func (c *Catalog) ChangesAfter(position SyncPosition, limit int) ([]ChangeRecord, SyncPosition, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateSyncLocked(position); err != nil {
		return nil, SyncPosition{}, false, err
	}
	limit = max(1, min(limit, 200))
	result := Journal{}
	next := position
	more := false
	for _, record := range c.journal.Records {
		if record.Sequence <= position.Sequence {
			continue
		}
		if len(result.Records) == limit {
			more = true
			break
		}
		result.Records = append(result.Records, record)
		next.Sequence, next.Hash = record.Sequence, record.Hash
	}
	return cloneJournal(result).Records, next, more, nil
}
