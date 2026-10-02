package metadata

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"ytmusic/internal/logger"
)

const defaultConfidenceThreshold = 0.7

// Resolver orchestrates metadata resolution: reads existing tags, normalizes,
// searches providers, scores results, and writes back the best metadata.
// When multiple providers are configured, the Resolver tries them in order for
// the primary match (fallback) and then fills missing fields from the remaining
// providers (gap filling).
type Resolver struct {
	providers          []Provider
	logger             *logger.Logger
	threshold          float64
	fingerprinter      Fingerprinter      // nil if not configured
	albumResolver      AlbumResolver      // nil if not configured
	batchFingerprinter BatchFingerprinter // nil if not configured
	releaseResolver    ReleaseResolver    // nil if not configured
	httpClient         *http.Client
	tags               *tagStore
	workers            int // files resolved at once in the per-file phase
}

// NewResolver creates a new Resolver with the given providers.
// If threshold is 0, the default (0.7) is used.
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

// WithWorkers sets how many files the per-file phase resolves at once.
// Returns the same Resolver to allow chaining.
func (r *Resolver) WithWorkers(n int) *Resolver {
	r.workers = max(n, 1)
	return r
}

// WithFingerprinter attaches an audio fingerprinter for pre-search identification.
// Returns the same Resolver to allow chaining.
func (r *Resolver) WithFingerprinter(f Fingerprinter) *Resolver {
	r.fingerprinter = f
	return r
}

// WithAlbumResolver attaches an album resolver for the album-first positional-tag phase.
// Returns the same Resolver to allow chaining.
func (r *Resolver) WithAlbumResolver(ar AlbumResolver) *Resolver {
	r.albumResolver = ar
	return r
}

// WithBatchFingerprinter attaches a batch fingerprinter for the batch-fingerprint phase.
func (r *Resolver) WithBatchFingerprinter(bf BatchFingerprinter) *Resolver {
	r.batchFingerprinter = bf
	return r
}

// WithReleaseResolver attaches a release resolver used by findDominantRelease.
func (r *Resolver) WithReleaseResolver(rr ReleaseResolver) *Resolver {
	r.releaseResolver = rr
	return r
}

// Resolve processes a list of audio file paths: for each file, it reads existing
// metadata, normalizes it, searches the provider, scores the best match, and
// writes improved metadata back if confident enough.
func (r *Resolver) Resolve(ctx context.Context, files []string) error {
	r.logger.Info("resolving metadata for %d files", len(files))

	groups := r.groupByAlbum(files)
	resolvedByA := make(map[string]bool)

	// Phase A: batch fingerprint → dominant release (writes positional tags)
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

	// Phase B: album-first text search (skips files already resolved by Phase A)
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
