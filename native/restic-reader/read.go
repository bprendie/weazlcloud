package main

import (
	"context"
	"io"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

func readFile(ctx context.Context, repo *repository.Repository, req request) (response, error) {
	found, err := findFile(ctx, repo, req.Snapshot, req.Object)
	if err != nil {
		return response{}, err
	}
	result := response{ID: req.ID, Size: found.Size, Body: make([]byte, 0, min(uint64(req.Limit), found.Size))}
	for _, blobID := range found.Content {
		if len(result.Body) >= req.Limit {
			break
		}
		blob, err := repo.LoadBlob(ctx, restic.DataBlob, blobID, nil)
		if err != nil {
			clear(result.Body)
			return response{}, err
		}
		result.Body = append(result.Body, blob[:min(len(blob), req.Limit-len(result.Body))]...)
		clear(blob)
	}
	if uint64(len(result.Body)) != min(uint64(req.Limit), found.Size) {
		clear(result.Body)
		return response{}, io.ErrUnexpectedEOF
	}
	return result, nil
}
