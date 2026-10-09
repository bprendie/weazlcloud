package library

import (
	"context"
	"errors"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/restic"
)

func (b *resticBackend) PutStreams(ctx context.Context, inputs []StreamInput) ([]catalog.Reference, error) {
	streams := make([]restic.StreamInput, len(inputs))
	for i, input := range inputs {
		streams[i] = restic.StreamInput{Name: input.Name, Size: input.Size, SHA256: input.Hash, Reader: input.Reader}
	}
	var refs []catalog.Reference
	err := b.withRepo(ctx, func(repo restic.Repo) error {
		snapshot, err := restic.PutStreams(ctx, repo, streams)
		if errors.Is(err, restic.ErrStreamsUnavailable) {
			return ErrPhotoStreamsUnavailable
		}
		if err != nil {
			return err
		}
		for _, input := range inputs {
			refs = append(refs, resticReference(snapshot, input.Name, ""))
		}
		return nil
	})
	return refs, err
}
