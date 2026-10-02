package app

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func (n *Node) shutdown() error {
	n.closeOnce.Do(func() { n.closeErr = n.shutdownResources() })
	return n.closeErr
}

func (n *Node) shutdownResources() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if n.idleCancel != nil {
		n.idleCancel()
	}
	if n.registry != nil {
		n.registry.StopAdmission()
	}
	// Close all listeners together; authenticated event streams have already
	// been canceled. Existing finite public transfers get the drain allowance.
	results := make(chan error, len(n.svcs))
	for _, server := range n.svcs {
		go func(server *http.Server) {
			err := server.Shutdown(ctx)
			if err != nil {
				_ = server.Close()
			}
			results <- err
		}(server)
	}
	var failure error
	for range n.svcs {
		failure = errors.Join(failure, <-results)
	}
	if n.idleDone != nil {
		select {
		case <-n.idleDone:
		case <-ctx.Done():
			failure = errors.Join(failure, ctx.Err())
		}
	}
	if n.registry != nil {
		failure = errors.Join(failure, n.registry.Shutdown(ctx))
	}
	if n.lib != nil {
		failure = errors.Join(failure, n.lib.Drain(ctx))
	}
	// Never close shared storage beneath handlers/jobs whose drain failed.
	if failure == nil && n.shared != nil {
		failure = n.shared.Close()
	}
	return failure
}
