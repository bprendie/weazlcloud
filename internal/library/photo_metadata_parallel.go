package library

import (
	"context"
	"sync"
	"time"
)

// Resolution is independent; only the coordinator publishes catalog mutations
// and the encrypted checkpoint. Refresh never runs alongside these readers.
func (l *Library) resolveMetadataBatch(ctx context.Context, resolver *metadataResolver, pending []PhotoMetadataEntry, options PhotoMetadataOptionsJob) []metadataResolved {
	results := make([]metadataResolved, len(pending))
	work := make(chan int)
	var workers sync.WaitGroup
	for i := 0; i < min(len(pending), metadataWorkers()); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range work {
				if ctx.Err() != nil {
					continue
				}
				workCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				results[index] = l.resolveMetadataEntry(workCtx, resolver, pending[index], options)
				cancel()
			}
		}()
	}
	for i := range pending {
		select {
		case work <- i:
		case <-ctx.Done():
		}
	}
	close(work)
	workers.Wait()
	return results
}

func metadataWorkers() int {
	return max(1, min(8, previewPolicy.BackgroundWorkers, previewPolicy.SourceReaders))
}
