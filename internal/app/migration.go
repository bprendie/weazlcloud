package app

import (
	"context"
	"io"

	"github.com/bprendie/weazlcloud/internal/migration"
)

func runMigration(ctx context.Context, dataDir, action string, output io.Writer) error {
	return migration.Run(ctx, dataDir, action, output)
}
