package metadata

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
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
	releaseFinder      ReleaseFinder
	httpClient         *http.Client
	tags               *tagStore
	workers            int
	fingerprintOff     *sync.Once // a pointer: forFile copies the Resolver, and the warning is once per run
	failures           *providerFailures
}

// What the per-file phase may still write to a file.
type scope int

const (
	scopeFile    scope = iota // no earlier phase placed it: everything
	scopeRelease              // phase A or B put it on a release: title, artist, genre and an ISRC it lacks
	scopeAlbum                // an album download: as scopeRelease, but the title stays too
)

// One search failing is a debug line; a provider failing all run long, as with wrong credentials, is worth a warning.
type providerFailures struct {
	mu     sync.Mutex
	counts map[string]int
	last   map[string]error
}

func (f *providerFailures) add(provider string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[provider]++
	f.last[provider] = err
}

func (f *providerFailures) report(log *logger.Logger) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, provider := range slices.Sorted(maps.Keys(f.counts)) {
		log.Warn("provider %s failed %d times, the last with: %v", provider, f.counts[provider], f.last[provider])
	}
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
		providers:      cached,
		logger:         log,
		threshold:      threshold,
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		tags:           &tagStore{},
		workers:        1,
		fingerprintOff: &sync.Once{},
		failures:       &providerFailures{counts: make(map[string]int), last: make(map[string]error)},
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

func (r *Resolver) WithReleaseFinder(rf ReleaseFinder) *Resolver {
	r.releaseFinder = rf
	return r
}

// album is the download as its source presents it, nil outside an album download.
func (r *Resolver) Resolve(ctx context.Context, files []string, album *Album) error {
	r.logger.Info("resolving metadata for %d files", len(files))

	var scopes map[string]scope
	if album != nil {
		r.resolveAlbum(ctx, album)
		scopes = r.albumMembers(files, album)
	} else {
		scopes = r.resolveGroups(ctx, files)
	}

	failed := r.resolveFiles(ctx, files, scopes)
	r.failures.report(r.logger)
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

// The album phase wrote their album-level fields; a file it did not cover gets the whole per-file phase.
func (r *Resolver) albumMembers(files []string, album *Album) map[string]scope {
	members := make(map[string]scope)
	for _, path := range albumPaths(album) {
		members[path] = scopeAlbum
	}
	for _, path := range files {
		if _, ok := members[path]; !ok {
			r.logger.Warn("album %q: %s has no position in it, resolving it on its own", album.Title, path)
		}
	}
	return members
}

// Phases A and B, which find a release per group of files sharing an album tag.
func (r *Resolver) resolveGroups(ctx context.Context, files []string) map[string]scope {
	groups := r.groupByAlbum(files)
	scopes := make(map[string]scope)

	// Phase A: batch fingerprint → dominant release.
	if r.batchFingerprinter != nil && r.releaseResolver != nil {
		for album, group := range groups {
			if album == "" || len(group) < 2 {
				continue
			}
			for _, path := range r.resolveGroupByFingerprint(ctx, group) {
				scopes[path] = scopeRelease
			}
		}
	}

	// Phase B: album-first search, for what phase A left.
	if r.albumResolver != nil {
		for album, group := range groups {
			if album == "" {
				continue
			}
			unresolved := filterResolved(group, scopes)
			if len(unresolved) == 0 {
				continue
			}
			placed, err := r.resolveGroup(ctx, album, unresolved, r.albumResolver)
			if err != nil {
				r.logger.Warn("album-first phase failed for %q: %v", album, err)
			}
			for _, path := range placed {
				scopes[path] = scopeRelease
			}
		}
	}
	return scopes
}
