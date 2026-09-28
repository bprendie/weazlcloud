package restic

import "context"

type memoryLimitKey struct{}

// WithMemoryLimit supplies a Go heap target for an individual preview child.
// GOMEMLIMIT is deliberately not presented as a hard RSS or container limit.
func WithMemoryLimit(ctx context.Context, bytes int64) context.Context {
	return context.WithValue(ctx, memoryLimitKey{}, bytes)
}
