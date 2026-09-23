package sharedstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	entries, err := os.ReadDir(filepath.Join(s.root, "shared-objects"))
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".wobj") || !validObjectID(strings.TrimSuffix(entry.Name(), ".wobj")) {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return out, e
		}
		out.AllocatedBytes += info.Size()
	}
	return out, nil
}
