package main

import (
	"context"
	"path"
	"strings"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

func readFile(ctx context.Context, repo *repository.Repository, req request) response {
	failed := response{ID: req.ID, Error: "source_unavailable"}
	id, err := restic.ParseID(req.Snapshot)
	if err != nil {
		return failed
	}
	snapshot, err := restic.LoadSnapshot(ctx, repo, id)
	if err != nil || snapshot.Tree == nil {
		return failed
	}
	parts := strings.Split(strings.TrimPrefix(path.Clean("/"+req.Object), "/"), "/")
	treeID := *snapshot.Tree
	for depth, part := range parts {
		tree, err := restic.LoadTree(ctx, repo, treeID)
		if err != nil {
			return failed
		}
		var found *restic.Node
		for _, node := range tree.Nodes {
			if node.Name == part {
				found = node
				break
			}
		}
		if found == nil {
			return failed
		}
		if depth < len(parts)-1 {
			if found.Type != restic.NodeTypeDir || found.Subtree == nil {
				return failed
			}
			treeID = *found.Subtree
			continue
		}
		if found.Type != restic.NodeTypeFile {
			return failed
		}
		result := response{ID: req.ID, Size: found.Size, Body: make([]byte, 0, min(uint64(req.Limit), found.Size))}
		for _, blobID := range found.Content {
			if len(result.Body) >= req.Limit {
				break
			}
			blob, err := repo.LoadBlob(ctx, restic.DataBlob, blobID, nil)
			if err != nil {
				clear(result.Body)
				return failed
			}
			result.Body = append(result.Body, blob[:min(len(blob), req.Limit-len(result.Body))]...)
			clear(blob)
		}
		if uint64(len(result.Body)) != min(uint64(req.Limit), found.Size) {
			clear(result.Body)
			return failed
		}
		return result
	}
	return failed
}
