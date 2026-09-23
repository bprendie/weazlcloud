package app

import (
	"context"

	"github.com/bprendie/weazlcloud/internal/filesvc"
)

func (n *Node) registerSharedMaintenance(registry *filesvc.Registry) {
	if n.shared == nil {
		return
	}
	n.activity.RegisterTask("shared-store-reconciliation-and-collection", func(ctx context.Context) (int64, error) {
		if err := registry.ReconcileShared(ctx); err != nil {
			return 0, err
		}
		return n.shared.Collect(ctx)
	})
}
