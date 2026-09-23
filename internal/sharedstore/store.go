package sharedstore

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type Store struct {
	root    string
	db      *sql.DB
	keys    nodeKeys
	options Options
}

func Open(root string, options Options) (*Store, error) {
	for _, dir := range []string{root, filepath.Join(root, "shared-objects"), filepath.Join(root, "shared-staging")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, err
		}
	}
	keys, err := loadKeys(filepath.Join(root, "shared-index"))
	if err != nil {
		return nil, err
	}
	db, err := openIndex(root)
	if err != nil {
		cryptox.Zero(keys.fingerprint)
		cryptox.Zero(keys.wrapping)
		return nil, err
	}
	s := &Store{root: root, db: db, keys: keys, options: options}
	if err := ensureChunkSettings(db); err != nil {
		s.Close()
		return nil, err
	}
	if _, err := db.Exec(`UPDATE objects SET state='deleting' WHERE state='building'`); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.cleanupStages(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.resumeDeletes(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.reconcileObjects(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	cryptox.Zero(s.keys.fingerprint)
	cryptox.Zero(s.keys.wrapping)
	return s.db.Close()
}

func (s *Store) cleanupStages() error {
	entries, err := os.ReadDir(filepath.Join(s.root, "shared-staging"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err = os.Remove(filepath.Join(s.root, "shared-staging", e.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *Store) reconcileObjects() error {
	rows, err := s.db.Query("SELECT object_id FROM objects WHERE state='ready'")
	if err != nil {
		return err
	}
	known := map[string]bool{}
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
		known[id+".wobj"] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	dir := filepath.Join(s.root, "shared-objects")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".wobj" {
			continue
		}
		if !known[entry.Name()] {
			if err = os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	for name := range known {
		if _, err = os.Stat(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("indexed shared object missing: %w", err)
		}
	}
	return nil
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	default:
		return c.r.Read(p)
	}
}
