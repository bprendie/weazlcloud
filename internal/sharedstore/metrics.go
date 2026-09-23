package sharedstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func (s *Store) Metrics(ctx context.Context) (Stats, error) {
	var out Stats
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM objects x WHERE x.state='ready' AND ((x.kind='whole' AND EXISTS(SELECT 1 FROM owners o WHERE o.object_id=x.object_id AND o.state='live')) OR (x.kind='chunk' AND EXISTS(SELECT 1 FROM object_dependencies d JOIN objects p ON p.object_id=d.parent_id JOIN owners o ON o.object_id=p.object_id WHERE d.child_id=x.object_id AND p.state='ready' AND o.state='live')))),
		coalesce((SELECT sum(x.plain_len) FROM objects x WHERE x.state='ready' AND ((x.kind='whole' AND EXISTS(SELECT 1 FROM owners o WHERE o.object_id=x.object_id AND o.state='live')) OR (x.kind='chunk' AND EXISTS(SELECT 1 FROM object_dependencies d JOIN objects p ON p.object_id=d.parent_id JOIN owners o ON o.object_id=p.object_id WHERE d.child_id=x.object_id AND p.state='ready' AND o.state='live')))),0),
		coalesce((SELECT sum(x.plain_len*(SELECT count(*) FROM owners o WHERE o.object_id=x.object_id AND o.state='live')) FROM objects x WHERE x.state='ready' AND x.kind IN ('whole','manifest') AND EXISTS(SELECT 1 FROM owners o WHERE o.object_id=x.object_id AND o.state='live')),0)
	`).Scan(&out.Objects, &out.UniqueBytes, &out.LogicalBytes)
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT object_id,kind FROM objects`)
	if err != nil {
		return out, err
	}
	kinds := make(map[string]string)
	for rows.Next() {
		var id, kind string
		if err = rows.Scan(&id, &kind); err != nil {
			rows.Close()
			return out, err
		}
		if !validObjectID(id) {
			rows.Close()
			return out, ErrState
		}
		kinds[id] = kind
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "shared-objects"))
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".wobj") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".wobj")
		if !validObjectID(id) {
			return out, ErrState
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return out, statErr
		}
		bytes := allocated(info)
		out.AllocatedBytes += bytes
		if kinds[id] == "manifest" {
			out.ManifestAllocated += bytes
		}
	}
	for _, name := range []string{"index.db", "index.db-wal", "index.db-shm"} {
		info, statErr := os.Stat(filepath.Join(s.root, "shared-index", name))
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return out, statErr
		}
		out.IndexAllocatedBytes += allocated(info)
	}
	out.AllocatedBytes += out.IndexAllocatedBytes
	if err = filepath.Walk(filepath.Join(s.root, "shared-staging"), func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode().IsRegular() {
			out.StagingAllocated += allocated(info)
		}
		return nil
	}); err != nil {
		return out, err
	}
	out.AllocatedBytes += out.StagingAllocated
	return out, nil
}

func allocated(info os.FileInfo) int64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return stat.Blocks * 512
	}
	return info.Size()
}
