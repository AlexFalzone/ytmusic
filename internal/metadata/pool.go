package metadata

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

// Only the per-file phase is parallel: the MusicBrainz rate limit serialises the others anyway.
func (r *Resolver) resolveFiles(ctx context.Context, files []string) int {
	indexes := make(chan int)
	var failed atomic.Int32
	var wg sync.WaitGroup

	for range min(r.workers, len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indexes {
				// The feeder's select may still hand out work after a cancel.
				if ctx.Err() != nil {
					continue
				}
				if err := r.forFile(i, len(files)).resolveSafely(ctx, files[i]); err != nil {
					failed.Add(1)
				}
			}
		}()
	}

	for i := range files {
		if ctx.Err() != nil {
			break
		}
		select {
		case indexes <- i:
		case <-ctx.Done():
		}
	}
	close(indexes)
	wg.Wait()

	return int(failed.Load())
}

// A copy whose log lines carry the file's position; everything else is shared.
func (r *Resolver) forFile(i, n int) *Resolver {
	fr := *r
	fr.logger = r.logger.WithPrefix(fmt.Sprintf("%d/%d", i+1, n))
	return &fr
}

func (r *Resolver) resolveSafely(ctx context.Context, path string) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("panic while resolving %s: %v\n%s", path, p, debug.Stack())
			err = fmt.Errorf("panic while resolving %s: %v", path, p)
		}
	}()

	r.logger.Debug("Processing: %s", path)
	if err := r.resolveFile(ctx, path); err != nil {
		r.logger.Warn("Failed to resolve metadata: %v", err)
		return err
	}
	return nil
}
