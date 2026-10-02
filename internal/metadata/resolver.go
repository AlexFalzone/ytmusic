package metadata

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"ytmusic/internal/logger"
)

const defaultConfidenceThreshold = 0.7

type Resolver struct {
	providers          []Provider
	logger             *logger.Logger
	threshold          float64
	fingerprinter      Fingerprinter
	albumResolver      AlbumResolver
	batchFingerprinter BatchFingerprinter
	releaseResolver    ReleaseResolver
	httpClient         *http.Client
	tags               *tagStore
	workers            int
}

func NewResolver(providers []Provider, log *logger.Logger, threshold float64) *Resolver {
	if threshold <= 0 {
		threshold = defaultConfidenceThreshold
	}
	cached := make([]Provider, len(providers))
	for i, p := range providers {
		cached[i] = &cachedProvider{Provider: p}
	}
	return &Resolver{
		providers:  cached,
		logger:     log,
		threshold:  threshold,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		tags:       &tagStore{},
		workers:    1,
	}
}

func (r *Resolver) WithWorkers(n int) *Resolver {
	r.workers = max(n, 1)
	return r
}

func (r *Resolver) WithFingerprinter(f Fingerprinter) *Resolver {
	r.fingerprinter = f
	return r
}

func (r *Resolver) WithAlbumResolver(ar AlbumResolver) *Resolver {
	r.albumResolver = ar
	return r
}

func (r *Resolver) WithBatchFingerprinter(bf BatchFingerprinter) *Resolver {
	r.batchFingerprinter = bf
	return r
}

func (r *Resolver) WithReleaseResolver(rr ReleaseResolver) *Resolver {
	r.releaseResolver = rr
	return r
}

func (r *Resolver) Resolve(ctx context.Context, files []string) error {
	r.logger.Info("resolving metadata for %d files", len(files))

	groups := r.groupByAlbum(files)
	resolvedByA := make(map[string]bool)

	// Phase A: batch fingerprint → dominant release.
	if r.batchFingerprinter != nil && r.releaseResolver != nil {
		for album, group := range groups {
			if album == "" || len(group) < 2 {
				continue
			}
			for _, p := range r.resolveGroupByFingerprint(ctx, group) {
				resolvedByA[p] = true
			}
		}
	}

	// Phase B: album-first search, for what phase A left.
	if r.albumResolver != nil {
		for album, group := range groups {
			if album == "" {
				continue
			}
			unresolved := filterResolved(group, resolvedByA)
			if len(unresolved) == 0 {
				continue
			}
			if err := r.resolveGroup(ctx, album, unresolved, r.albumResolver); err != nil {
				r.logger.Warn("album-first phase failed for %q: %v", album, err)
			}
		}
	}

	failed := r.resolveFiles(ctx, files)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resolving metadata: %w", err)
	}

	if failed == len(files) {
		return fmt.Errorf("all %d files failed metadata resolution", len(files))
	}

	if failed > 0 {
		r.logger.Warn("%d of %d files failed metadata resolution", failed, len(files))
	}

	r.logger.Info("Metadata resolution completed")
	return nil
}
