package metadata

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// resolveFiles runs the per-file phase on a pool of workers and returns how
// many files failed. The phases before it stay sequential: the MusicBrainz
// rate limit would serialise them anyway.
func (r *Resolver) resolveFiles(ctx context.Context, files []string) int {
	indexes := make(chan int)
	var failed atomic.Int32
	var wg sync.WaitGroup

	for range min(r.workers, len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indexes {
				// The feeder checks too, but once a cancel and a waiting
				// worker are both ready its select may pick either.
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

// forFile returns a copy of the resolver whose log lines carry the file's
// position, so the lines of files resolved side by side can be told apart.
// Everything else is shared: providers, caches, the tag store.
func (r *Resolver) forFile(i, n int) *Resolver {
	fr := *r
	fr.logger = r.logger.WithPrefix(fmt.Sprintf("%d/%d", i+1, n))
	return &fr
}

// resolveSafely resolves one file and turns a panic into its failure. The
// workers run off any handler stack: a panic there would take the whole
// process down, and with it every job of the web server.
func (r *Resolver) resolveSafely(ctx context.Context, path string) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("panic while resolving %s: %v", path, p)
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
