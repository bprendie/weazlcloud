package main

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

func findFile(ctx context.Context, repo *repository.Repository, snapshotID, object string) (*restic.Node, error) {
	unavailable := errors.New("source unavailable")
	id, err := restic.ParseID(snapshotID)
	if err != nil {
		return nil, unavailable
	}
	snapshot, err := restic.LoadSnapshot(ctx, repo, id)
	if err != nil || snapshot.Tree == nil {
		return nil, unavailable
	}
	parts := strings.Split(strings.TrimPrefix(path.Clean("/"+object), "/"), "/")
	treeID := *snapshot.Tree
	for depth, part := range parts {
		tree, err := restic.LoadTree(ctx, repo, treeID)
		if err != nil {
			return nil, unavailable
		}
		var found *restic.Node
		for _, node := range tree.Nodes {
			if node.Name == part {
				found = node
				break
			}
		}
		if found == nil {
			return nil, unavailable
		}
		if depth == len(parts)-1 {
			if found.Type != restic.NodeTypeFile {
				return nil, unavailable
			}
			return found, nil
		}
		if found.Type != restic.NodeTypeDir || found.Subtree == nil {
			return nil, unavailable
		}
		treeID = *found.Subtree
	}
	return nil, unavailable
}
