package sharedstore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

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
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := []string{
		`CREATE TABLE objects (object_id TEXT PRIMARY KEY, fingerprint BLOB NOT NULL UNIQUE, plain_len INTEGER NOT NULL, node_key BLOB NOT NULL, state TEXT NOT NULL, write_op TEXT NOT NULL)`,
		`CREATE TABLE operations (op_id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, entry_id TEXT NOT NULL, revision INTEGER NOT NULL, object_id TEXT NOT NULL, state TEXT NOT NULL, UNIQUE(owner_id,entry_id,revision))`,
		`CREATE TABLE owners (owner_id TEXT NOT NULL, entry_id TEXT NOT NULL, revision INTEGER NOT NULL, object_id TEXT NOT NULL REFERENCES objects(object_id), wrapped_key BLOB NOT NULL, op_id TEXT NOT NULL, state TEXT NOT NULL, PRIMARY KEY(owner_id,entry_id,revision))`,
		`CREATE INDEX owners_object ON owners(object_id,state)`,
	}
	for _, q := range queries {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("PRAGMA user_version=1"); err != nil {
		return err
	}
	return tx.Commit()
}
