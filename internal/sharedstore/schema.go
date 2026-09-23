package sharedstore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schemaVersion = 5

func openIndex(root string) (*sql.DB, error) {
	dir := filepath.Join(root, "shared-index")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "index.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(dir, "index.db"), 0o600); err != nil {
		db.Close()
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err = os.Chmod(filepath.Join(dir, "index.db")+suffix, 0o600); err != nil && !os.IsNotExist(err) {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	var version int
	err := db.QueryRow("PRAGMA user_version").Scan(&version)
	if err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("unsupported shared index schema %d", version)
	}
	if version == schemaVersion {
		return nil
	}
	if version == 1 {
		if _, err = db.Exec(`CREATE TABLE holds (hold_id TEXT PRIMARY KEY, object_id TEXT NOT NULL REFERENCES objects(object_id), created_at INTEGER NOT NULL)`); err != nil {
			return err
		}
		version = 2
	}
	if version == 2 {
		for _, q := range []string{`ALTER TABLE holds ADD COLUMN owner_id BLOB NOT NULL DEFAULT X''`, `ALTER TABLE holds ADD COLUMN entry_id TEXT NOT NULL DEFAULT ''`, `ALTER TABLE holds ADD COLUMN revision INTEGER NOT NULL DEFAULT 0`, `ALTER TABLE holds ADD COLUMN operation TEXT NOT NULL DEFAULT ''`} {
			if _, err = db.Exec(q); err != nil {
				return err
			}
		}
		if _, err = db.Exec("PRAGMA user_version=3"); err != nil {
			return err
		}
		version = 3
	}
	if version == 3 {
		tx, txErr := db.Begin()
		if txErr != nil {
			return txErr
		}
		defer tx.Rollback()
		for _, q := range []string{
			`ALTER TABLE objects ADD COLUMN kind TEXT NOT NULL DEFAULT 'whole'`,
			`ALTER TABLE operations ADD COLUMN content_hash BLOB`,
			`CREATE TABLE object_dependencies(parent_id TEXT NOT NULL REFERENCES objects(object_id) ON DELETE CASCADE, child_id TEXT NOT NULL REFERENCES objects(object_id) ON DELETE CASCADE, PRIMARY KEY(parent_id,child_id))`,
			`CREATE INDEX object_dependencies_child ON object_dependencies(child_id)`,
			`CREATE TABLE format_settings(setting TEXT PRIMARY KEY, value TEXT NOT NULL)`,
			`PRAGMA user_version=4`,
		} {
			if _, txErr = tx.Exec(q); txErr != nil {
				return txErr
			}
		}
		if txErr = tx.Commit(); txErr != nil {
			return txErr
		}
		version = 4
	}
	if version == 4 {
		for _, q := range []string{`ALTER TABLE objects ADD COLUMN stored_len INTEGER NOT NULL DEFAULT 0`, `ALTER TABLE objects ADD COLUMN encoding TEXT NOT NULL DEFAULT 'raw'`, `PRAGMA user_version=5`} {
			if _, err = db.Exec(q); err != nil {
				return err
			}
		}
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := []string{
		`CREATE TABLE objects (object_id TEXT PRIMARY KEY, fingerprint BLOB NOT NULL UNIQUE, plain_len INTEGER NOT NULL, stored_len INTEGER NOT NULL DEFAULT 0, encoding TEXT NOT NULL DEFAULT 'raw', node_key BLOB NOT NULL, state TEXT NOT NULL, write_op TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'whole')`,
		`CREATE TABLE operations (op_id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, entry_id TEXT NOT NULL, revision INTEGER NOT NULL, object_id TEXT NOT NULL, state TEXT NOT NULL, content_hash BLOB, UNIQUE(owner_id,entry_id,revision))`,
		`CREATE TABLE owners (owner_id TEXT NOT NULL, entry_id TEXT NOT NULL, revision INTEGER NOT NULL, object_id TEXT NOT NULL REFERENCES objects(object_id), wrapped_key BLOB NOT NULL, op_id TEXT NOT NULL, state TEXT NOT NULL, PRIMARY KEY(owner_id,entry_id,revision))`,
		`CREATE INDEX owners_object ON owners(object_id,state)`,
		`CREATE TABLE holds (hold_id TEXT PRIMARY KEY, object_id TEXT NOT NULL REFERENCES objects(object_id), owner_id BLOB NOT NULL, entry_id TEXT NOT NULL, revision INTEGER NOT NULL, operation TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`CREATE TABLE object_dependencies(parent_id TEXT NOT NULL REFERENCES objects(object_id) ON DELETE CASCADE, child_id TEXT NOT NULL REFERENCES objects(object_id) ON DELETE CASCADE, PRIMARY KEY(parent_id,child_id))`,
		`CREATE INDEX object_dependencies_child ON object_dependencies(child_id)`,
		`CREATE TABLE format_settings(setting TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	}
	for _, q := range queries {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("PRAGMA user_version=5"); err != nil {
		return err
	}
	return tx.Commit()
}
