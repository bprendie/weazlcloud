package library

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func (l *Library) metadataDryPath() string {
	return filepath.Join(filepath.Dir(l.repo), ".weazl-photo-metadata-dry-run.enc")
}

// Inspection keeps its own encrypted checkpoint. Durable sequences select the
// latest job after restart without relying on filesystem or wall-clock ordering.
func (l *Library) readMetadataCheckpoint(name string) (*PhotoMetadataJob, error) {
	f, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 64<<20+1))
	if err != nil || len(raw) > 64<<20 {
		return nil, errors.New("metadata job exceeds size limit")
	}
	plain, err := l.vault.Unwrap(raw)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var disk metadataJobDisk
	if json.Unmarshal(plain, &disk) != nil || disk.Job.Version != 1 || disk.Job.Parser != "capture-v2" || len(disk.Entries) > 200000 {
		return nil, errors.New("invalid metadata job")
	}
	disk.Job.Entries = disk.Entries
	return &disk.Job, nil
}
