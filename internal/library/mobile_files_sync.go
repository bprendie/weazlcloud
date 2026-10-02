package library

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
)

var mobileFilesBoot = rand.Text()

type mobileFilesCursor struct {
	Position catalog.SyncPosition `json:"position"`
	Mode     string               `json:"mode"`
	After    string               `json:"after,omitempty"`
	Prefix   string               `json:"prefix"`
	Device   string               `json:"device"`
	Session  uint64               `json:"session"`
	Boot     string               `json:"boot"`
	Expires  int64                `json:"expires"`
}

type MobileFilesChange struct {
	Sequence uint64          `json:"sequence"`
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Deleted  bool            `json:"deleted,omitempty"`
	Item     *MobileFileItem `json:"item,omitempty"`
}

type MobileFilesPage struct {
	Items          []MobileFileItem    `json:"items"`
	Changes        []MobileFilesChange `json:"changes,omitempty"`
	NextCursor     string              `json:"next_cursor,omitempty"`
	Checkpoint     string              `json:"checkpoint,omitempty"`
	HasMore        bool                `json:"has_more"`
	ResyncRequired bool                `json:"resync_required"`
}

func (l *Library) FilesSync(ctx context.Context, device, raw, prefix string, limit int) (MobileFilesPage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return MobileFilesPage{}, err
	}
	if device == "" || len(device) > 128 {
		return MobileFilesPage{}, catalog.ErrSyncExpired
	}
	if prefix != "" {
		clean, err := cleanPath(prefix)
		if err != nil || clean != prefix {
			return MobileFilesPage{}, catalog.ErrSyncExpired
		}
	}
	limit = max(1, min(limit, 200))
	var cursor mobileFilesCursor
	var err error
	if raw == "" {
		position, e := l.catalog.SyncPosition()
		if e != nil {
			return MobileFilesPage{}, e
		}
		cursor = l.newFilesCursor(device, prefix, position)
		cursor.Mode = "snapshot"
	} else {
		cursor, err = l.decodeFilesCursor(raw, device, prefix)
		if err != nil {
			return MobileFilesPage{}, err
		}
	}
	page := MobileFilesPage{Items: []MobileFileItem{}}
	if cursor.Mode == "snapshot" {
		rows, more := l.catalog.MobileFilesSnapshot(cursor.After, prefix, limit)
		for _, f := range rows {
			page.Items = append(page.Items, mobileFileItem(f))
		}
		page.HasMore = more
		if more {
			cursor.After = rows[len(rows)-1].EntryID
		} else {
			cursor.Mode, cursor.After = "delta", ""
		}
	} else {
		records, next, more, e := l.catalog.ChangesAfter(cursor.Position, limit)
		if e != nil {
			return MobileFilesPage{}, e
		}
		for _, record := range records {
			if record.Kind != "asset" && record.Kind != "folder" {
				continue
			}
			change := MobileFilesChange{Sequence: record.Sequence, Kind: record.Kind, ID: record.ID, Deleted: record.Deleted}
			// Out-of-filter moves/removals are tombstones. The client may have
			// seen the ID before it moved, even when no current row remains.
			if record.File == nil || !catalog.MobileFilesMatch(record.File.Path, prefix) {
				change.Deleted = true
			}
			if !change.Deleted {
				item := mobileFileItem(*record.File)
				change.Item = &item
			}
			page.Changes = append(page.Changes, change)
		}
		cursor.Position, page.HasMore = next, more
	}
	if page.HasMore {
		page.NextCursor, err = l.encodeFilesCursor(cursor)
	} else {
		page.Checkpoint, err = l.encodeFilesCursor(cursor)
	}
	return page, err
}

func (l *Library) newFilesCursor(device, prefix string, position catalog.SyncPosition) mobileFilesCursor {
	return mobileFilesCursor{Position: position, Mode: "delta", Prefix: prefix, Device: device, Session: l.catalogSession, Boot: mobileFilesBoot, Expires: time.Now().Add(24 * time.Hour).Unix()}
}

func (l *Library) encodeFilesCursor(cursor mobileFilesCursor) (string, error) {
	plain, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	defer clear(plain)
	raw, err := l.vault.Wrap(plain)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (l *Library) decodeFilesCursor(raw, device, prefix string) (mobileFilesCursor, error) {
	var cursor mobileFilesCursor
	if len(raw) > 8192 {
		return cursor, catalog.ErrSyncExpired
	}
	wrapped, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, catalog.ErrSyncExpired
	}
	plain, err := l.vault.Unwrap(wrapped)
	if err != nil {
		return cursor, catalog.ErrSyncExpired
	}
	defer clear(plain)
	if json.Unmarshal(plain, &cursor) != nil || cursor.Device != device || cursor.Prefix != prefix || cursor.Boot != mobileFilesBoot || cursor.Session != l.catalogSession || cursor.Expires <= time.Now().Unix() || len(cursor.After) > 128 || cursor.Mode != "snapshot" && cursor.Mode != "delta" || cursor.Position.Epoch == "" {
		return cursor, catalog.ErrSyncExpired
	}
	return cursor, l.catalog.ValidateSync(cursor.Position)
}

func filesDeviceKey(device, prefix string) string {
	hash := sha256.Sum256([]byte(prefix))
	return "files:" + device + ":" + hex.EncodeToString(hash[:8])
}

func (l *Library) AcknowledgeFilesSync(ctx context.Context, device, raw, prefix string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return err
	}
	cursor, err := l.decodeFilesCursor(raw, device, prefix)
	if err != nil || cursor.Mode != "delta" {
		return catalog.ErrSyncExpired
	}
	return l.catalog.AcknowledgeDevice(filesDeviceKey(device, prefix), cursor.Position)
}

func (l *Library) FilesDeviceCheckpoint(ctx context.Context, device, prefix string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensure(ctx); err != nil {
		return "", err
	}
	position, err := l.catalog.DeviceCheckpoint(filesDeviceKey(device, prefix))
	if err != nil {
		return "", err
	}
	return l.encodeFilesCursor(l.newFilesCursor(device, prefix, position))
}
